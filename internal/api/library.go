package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/autosearch"
	"github.com/librinode/librinode/internal/comiccover"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
	"github.com/librinode/librinode/internal/scanner"
)

const metadataTimeout = 60 * time.Second

func (s *server) metadataCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), metadataTimeout)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// writeStoreError maps store errors to HTTP responses.
func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

const notConfiguredMsg = "no metadata provider configured — add a provider token under Settings"

// writeSyncError maps refresh-service errors to HTTP responses.
func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, notConfiguredMsg)
	case errors.Is(err, library.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, metadata.ErrNotFound):
		// err.Error() carries specifics when available (e.g. a provider-override
		// refresh that couldn't re-match the record by name on the new
		// provider) — more useful than a bare, generic "not found".
		msg := err.Error()
		if msg == metadata.ErrNotFound.Error() {
			msg = "not found at metadata provider"
		}
		writeError(w, http.StatusNotFound, msg)
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// --- Search (metadata provider proxy) ---

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	term := r.URL.Query().Get("term")
	if term == "" {
		writeError(w, http.StatusBadRequest, "term is required")
		return
	}
	kind := r.URL.Query().Get("type")
	if kind == "" {
		kind = "book"
	}

	// Series-first types go to their own providers.
	if mediaType, ok := seriesMediaType(kind); ok {
		s.handleSearchSeries(w, r, mediaType, term)
		return
	}

	provider := s.metadata.Current()
	if provider == nil {
		writeError(w, http.StatusServiceUnavailable, notConfiguredMsg)
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	switch kind {
	case "author":
		results, err := provider.SearchAuthors(ctx, term)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	case "book":
		results, err := provider.SearchBooks(ctx, term)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, results)
	default:
		writeError(w, http.StatusBadRequest, "type must be author, book, or comic")
	}
}

// --- Authors ---

func (s *server) handleListAuthors(w http.ResponseWriter, r *http.Request) {
	var authors []library.Author
	var err error
	if lib := r.URL.Query().Get("library"); lib != "" {
		if _, ok := formatLibrary(lib); !ok {
			writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
			return
		}
		authors, err = s.store.ListAuthorsInLibrary(lib)
	} else {
		authors, err = s.store.ListAuthors()
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, authors)
}

// handleAddAuthor syncs an author from the metadata provider with their full
// bibliography and makes the author a member of the chosen format library.
// Books are NOT auto-enrolled: they land in the author's Missing section for
// the user to monitor selectively (owning files enrolls them automatically).
func (s *server) handleAddAuthor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ForeignAuthorID string `json:"foreignAuthorId"`
		Monitored       *bool  `json:"monitored"`
		Library         string `json:"library"` // which format library to add into
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ForeignAuthorID == "" {
		writeError(w, http.StatusBadRequest, "foreignAuthorId is required")
		return
	}
	monitored := true
	if req.Monitored != nil {
		monitored = *req.Monitored
	}
	lib, ok := formatLibrary(req.Library)
	if !ok {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	author, err := s.refresh.SyncAuthor(ctx, req.ForeignAuthorID, monitored)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if err := s.store.SetAuthorLibrary(author.ID, lib, true); err != nil {
		writeStoreError(w, err)
		return
	}
	s.rematchFiles()
	s.prefetchAuthorImages(author.ID)
	s.writeAuthorDetail(w, http.StatusCreated, author.ID)
}

// handleAuthorLibrary removes an author from ONE format library: the author
// flag and all their books' membership in that format are cleared (the other
// library is untouched), optionally deleting that format's files. When the
// author is in no library afterwards, the whole record is deleted.
func (s *server) handleAuthorLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Library     string `json:"library"`
		Member      bool   `json:"member"`
		DeleteFiles bool   `json:"deleteFiles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	lib, ok := formatLibrary(req.Library)
	if !ok || req.Library == "" {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}
	if req.Member {
		if err := s.store.SetAuthorLibrary(id, lib, true); err != nil {
			writeStoreError(w, err)
			return
		}
		s.writeAuthorDetail(w, http.StatusOK, id)
		return
	}

	var paths []string
	if req.DeleteFiles {
		var err error
		if paths, err = s.store.FilePathsForAuthorFormat(id, lib); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := s.store.SetAuthorLibrary(id, lib, false); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.RemoveAuthorBooksLibrary(id, lib); err != nil {
		writeStoreError(w, err)
		return
	}
	if req.DeleteFiles {
		if _, errs := s.removeFilesFromDisk(paths); len(errs) > 0 {
			slog.Warn("deleting files on author library removal", "authorId", id, "errors", strings.Join(errs, "; "))
		}
		if err := s.store.DeleteAuthorBookFilesForFormat(id, lib); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	// Gone from both libraries → nothing left to show anywhere; delete the
	// author record entirely (books cascade).
	author, err := s.store.GetAuthor(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !author.InEbookLibrary && !author.InAudiobookLibrary {
		if err := s.store.DeleteAuthor(id); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	writeJSON(w, http.StatusOK, author)
}

// formatLibrary validates the target format library ("" defaults to ebook).
func formatLibrary(v string) (string, bool) {
	switch v {
	case "", "ebook":
		return "ebook", true
	case "audiobook":
		return "audiobook", true
	}
	return "", false
}

// rematchFiles attaches previously scanned unmatched files to newly added
// books, so "add the book" is all a user needs to do after a scan.
func (s *server) rematchFiles() {
	if _, err := s.scanner.RematchUnmatched(); err != nil {
		slog.Warn("rematching unmatched files", "error", err)
	}
}

// handleRefreshLibrary starts a library-wide metadata refresh in the
// background — the bulk twin of the per-author/per-series Refresh buttons,
// honoring provider overrides the same way:
// POST /api/v1/library/refresh {"mediaType":"ebook"}. One runs at a time; a
// second request while one is running gets a 409.
func (s *server) handleRefreshLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Count what the refresh will cover so the response can say so.
	count := 0
	switch req.MediaType {
	case "ebook", "audiobook":
		authors, err := s.store.ListAuthors()
		if err != nil {
			writeStoreError(w, err)
			return
		}
		for i := range authors {
			if (req.MediaType == "ebook" && authors[i].InEbookLibrary) ||
				(req.MediaType == "audiobook" && authors[i].InAudiobookLibrary) {
				count++
			}
		}
	case "comic":
		seriesList, err := s.store.ListSeries(req.MediaType)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		count = len(seriesList)
	case "all":
		// Every author (prose) plus every comic series — the whole library.
		authors, err := s.store.ListAuthors()
		if err != nil {
			writeStoreError(w, err)
			return
		}
		count = len(authors)
		if seriesList, err := s.store.ListSeries("comic"); err == nil {
			count += len(seriesList)
		}
	default:
		writeError(w, http.StatusBadRequest, "mediaType must be ebook, audiobook, comic, or all")
		return
	}
	if count == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"started": 0, "message": "Nothing to refresh — the library is empty.",
		})
		return
	}

	if !s.libRefreshBusy.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a library metadata refresh is already running")
		return
	}
	mediaType := req.MediaType
	go func() {
		defer s.libRefreshBusy.Store(false)
		// Detached from the request: a big library takes a while, and closing
		// the browser tab must not abort the sweep.
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if mediaType == "all" {
			s.refresh.RefreshAll(ctx)
			return
		}
		if _, err := s.refresh.RefreshLibrary(ctx, mediaType); err != nil {
			slog.Warn("library metadata refresh", "mediaType", mediaType, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": count,
		"message": fmt.Sprintf(
			"Refreshing metadata for %d record(s) in the background — changes appear as they complete.", count),
	})
}

// handleRefreshAuthor re-syncs an existing author (and bibliography) from the
// metadata provider.
func (s *server) handleRefreshAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	if err := s.refresh.RefreshAuthor(ctx, id); err != nil {
		writeSyncError(w, err)
		return
	}
	s.prefetchAuthorImages(id)
	s.writeAuthorDetail(w, http.StatusOK, id)
}

func (s *server) handleGetAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeAuthorDetail(w, http.StatusOK, id)
}

func (s *server) writeAuthorDetail(w http.ResponseWriter, status int, id int64) {
	author, err := s.store.GetAuthor(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	books, err := s.store.ListBooks(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	author.Books = books
	writeJSON(w, status, author)
}

func (s *server) handleMonitorAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Monitored bool `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetAuthorMonitored(id, req.Monitored); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "monitored": req.Monitored})
}

// handleAuthorProvider sets (or with "" clears) the author's per-record
// provider override — it beats the global Settings → Metadata selection on
// the next refresh, including a disabled ("None") book provider.
func (s *server) handleAuthorProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Provider != "" && !slices.Contains(metadata.Available(), req.Provider) {
		writeError(w, http.StatusBadRequest, "unknown provider: "+req.Provider)
		return
	}
	if err := s.store.SetAuthorProviderOverride(id, req.Provider); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeAuthorDetail(w, http.StatusOK, id)
}

func (s *server) handleDeleteAuthor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)
	var paths []string
	if deleteFiles {
		var err error
		if paths, err = s.store.FilePathsForAuthor(id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := s.store.DeleteAuthor(id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.finishDelete(w, deleteFiles, paths)
}

// --- Books ---

func (s *server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	// ?library=ebook|audiobook scopes to that format's member books, filtered
	// server-side (the Ebooks/Audiobooks page's manual-match fallback list —
	// no reason to ship every book of every media type for that). Mutually
	// exclusive with ?authorId=; authorId wins if both are somehow given.
	var authorID int64
	if v := r.URL.Query().Get("authorId"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid authorId")
			return
		}
		authorID = id
	}
	if authorID > 0 {
		// The author page groups/sorts its grid by series, so it needs the books'
		// series links (plain ListBooks omits them).
		books, err := s.store.ListAuthorBooks(authorID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, books)
		return
	}
	if lib := r.URL.Query().Get("library"); lib != "" {
		if lib != "ebook" && lib != "audiobook" {
			writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
			return
		}
		books, err := s.store.ListBooksInLibrary(lib)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, books)
		return
	}
	books, err := s.store.ListBooks(0)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, books)
}

// handleAddBook syncs one book (with editions) from the metadata provider.
// The author is created as an unmonitored stub when not in the library yet;
// the book joins the library named in the request ("ebook" by default).
func (s *server) handleAddBook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ForeignBookID string `json:"foreignBookId"`
		Monitored     *bool  `json:"monitored"`
		Library       string `json:"library"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ForeignBookID == "" {
		writeError(w, http.StatusBadRequest, "foreignBookId is required")
		return
	}
	monitored := true
	if req.Monitored != nil {
		monitored = *req.Monitored
	}
	lib, ok := formatLibrary(req.Library)
	if !ok {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	book, err := s.refresh.SyncBook(ctx, req.ForeignBookID, monitored)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	// Re-adds enroll the existing book too (upserts preserve membership).
	if err := s.store.SetBookLibrary(book.ID, lib, true, monitored); err != nil {
		writeStoreError(w, err)
		return
	}
	s.rematchFiles()
	s.images.Prefetch(book.CoverURL)
	s.writeBookDetail(w, http.StatusCreated, book.ID)
}

// handleBookLibrary is the cross-add/remove: PUT /book/{id}/library puts a
// prose book into (or out of) a format library, with its monitored choice.
func (s *server) handleBookLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Library   string `json:"library"`
		Member    bool   `json:"member"`
		Monitored bool   `json:"monitored"`
		// DeleteFiles removes this format's files from disk when leaving
		// the library (ignored when member is true).
		DeleteFiles bool `json:"deleteFiles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Comic issues have no per-format membership column — one is in the
	// library when it's monitored or owned. member=true monitors it (adds it
	// back from the series' Missing section); member=false removes it: it
	// unmonitors, and its file records are forgotten so it's no longer owned
	// and drops into Missing (optionally deleting the files from disk first
	// — otherwise the next scan re-finds them, like any other library).
	if req.Library == "comic" {
		if !req.Member {
			if req.DeleteFiles {
				paths, err := s.store.FilePathsForBookFormat(id, req.Library)
				if err != nil {
					writeStoreError(w, err)
					return
				}
				if _, errs := s.removeFilesFromDisk(paths); len(errs) > 0 {
					slog.Warn("deleting volume files on removal", "bookId", id, "library", req.Library, "errors", strings.Join(errs, "; "))
				}
			}
			if err := s.store.DeleteBookFilesForFormat(id, req.Library); err != nil {
				writeStoreError(w, err)
				return
			}
		}
		if err := s.store.SetBookMonitored(id, req.Member); err != nil {
			writeStoreError(w, err)
			return
		}
		s.writeBookDetail(w, http.StatusOK, id)
		return
	}
	lib, ok := formatLibrary(req.Library)
	if !ok || req.Library == "" {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}
	deleteFiles := req.DeleteFiles && !req.Member
	var paths []string
	if deleteFiles {
		var err error
		if paths, err = s.store.FilePathsForBookFormat(id, lib); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := s.store.SetBookLibrary(id, lib, req.Member, req.Monitored); err != nil {
		writeStoreError(w, err)
		return
	}
	if deleteFiles {
		if _, errs := s.removeFilesFromDisk(paths); len(errs) > 0 {
			slog.Warn("deleting files on library removal", "bookId", id, "errors", strings.Join(errs, "; "))
		}
		// The book row survives, so its file rows must go explicitly (disk
		// deletion alone would leave them stale until the next scan).
		if err := s.store.DeleteBookFilesForFormat(id, lib); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	s.writeBookDetail(w, http.StatusOK, id)
}

// handleLibraries reports which media-type libraries are set up (Plex-style:
// the UI only shows active ones).
func (s *server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.store.LibraryStatuses()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, statuses)
}

// handleHome builds the Home page: per-library sections, active libraries
// only, types never mixed within a row.
func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	sections, err := s.store.Home(12)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sections)
}

// handleAuthorSearch sweeps ONE author's wanted books in a format library —
// the author page's Search wanted button (scoped: other authors untouched).
func (s *server) handleAuthorSearch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lib, ok := formatLibrary(r.URL.Query().Get("library"))
	if !ok {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}
	books, err := s.store.ListBooks(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	wanted := func(b *library.Book) bool {
		if lib == "audiobook" {
			return b.InAudiobookLibrary && b.AudiobookMonitored && !b.HasAudiobookFile
		}
		return b.InEbookLibrary && b.EbookMonitored && !b.HasEbookFile
	}
	outcomes := []any{}
	searched, grabbed := 0, 0
	for i := range books {
		if !wanted(&books[i]) {
			continue
		}
		// Pace between searches so an author with a long wanted list doesn't
		// fire a rapid burst that trips indexers (or Prowlarr) into a
		// rate-limit backoff — same cadence as the library-wide sweep.
		if searched > 0 {
			select {
			case <-r.Context().Done():
				writeJSON(w, http.StatusOK, map[string]any{
					"searched": searched, "grabbed": grabbed, "outcomes": outcomes,
				})
				return
			case <-time.After(autosearch.WantedSearchPacing):
			}
		}
		searched++
		o, err := s.search.SearchBook(r.Context(), books[i].ID, lib)
		if err != nil {
			outcomes = append(outcomes, map[string]any{
				"bookId": books[i].ID, "bookTitle": books[i].Title, "grabbed": false, "message": err.Error(),
			})
			continue
		}
		if o.Grabbed {
			grabbed++
		}
		outcomes = append(outcomes, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"searched": searched, "grabbed": grabbed, "outcomes": outcomes,
	})
}

// handleAuthorMissing lists an author's bibliography gaps for one format
// library — prose books neither monitored nor owned there (hidden from the
// Books grid) — for the author page's Missing section.
func (s *server) handleAuthorMissing(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	lib, ok := formatLibrary(r.URL.Query().Get("library"))
	if !ok {
		writeError(w, http.StatusBadRequest, "library must be ebook or audiobook")
		return
	}
	books, err := s.store.MissingForAuthor(id, lib)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, books)
}

// handleWanted lists one library's wanted items — monitored but missing
// that format's file — for the per-library Wanted page.
func (s *server) handleWanted(w http.ResponseWriter, r *http.Request) {
	mediaType := r.URL.Query().Get("library")
	if !slices.Contains(library.MediaTypes, mediaType) {
		writeError(w, http.StatusBadRequest, "library must be one of: "+strings.Join(library.MediaTypes, ", "))
		return
	}
	items, err := s.store.Wanted(mediaType)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleCalendar returns dated releases across all libraries. ?past= and
// ?days= (defaults 30/90) bound the window around today.
func (s *server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	intParam := func(name string, def, max int) int {
		v, err := strconv.Atoi(r.URL.Query().Get(name))
		if err != nil || v < 0 {
			return def
		}
		return min(v, max)
	}
	past := intParam("past", 30, 365)
	days := intParam("days", 90, 365)
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -past).Format("2006-01-02")
	to := now.AddDate(0, 0, days).Format("2006-01-02")
	items, err := s.store.Calendar(from, to)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "from": from, "to": to})
}

// handleRefreshBook re-syncs an existing book's metadata and editions.
func (s *server) handleRefreshBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	if err := s.refresh.RefreshBook(ctx, id); err != nil {
		writeSyncError(w, err)
		return
	}
	s.writeBookDetail(w, http.StatusOK, id)
}

func (s *server) handleGetBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeBookDetail(w, http.StatusOK, id)
}

// handleBookCover streams the cover image extracted from one of a book's
// comic archives (CBZ/CBR) — a real cover for owned comic volumes.
// The extracted image is cached on disk (<data>/covers) and re-used until a
// source file changes. 404 when the book has no comic file or none yields an
// image; the UI then falls back to the provider cover.
func (s *server) handleBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	book, err := s.store.GetBook(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// When this library prefers provider art, don't extract from the file —
	// 404 lets the UI fall back to the provider cover.
	if s.cfg.UseProviderCovers(book.MediaType) {
		writeError(w, http.StatusNotFound, "provider covers preferred")
		return
	}
	files, err := s.store.ListBookFiles(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Newest comic-file mtime — the cache is valid only while it's at least
	// this fresh, so replacing a volume's file refreshes its cover.
	var comicFiles []library.BookFile
	var newest time.Time
	for _, f := range files {
		if f.MediaType != "comic" {
			continue
		}
		comicFiles = append(comicFiles, f)
		if info, err := os.Stat(f.Path); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if len(comicFiles) == 0 {
		writeError(w, http.StatusNotFound, "no cover available")
		return
	}

	cachePath := filepath.Join(s.cfg.DataDir(), "covers", fmt.Sprintf("book-%d", id))
	if info, err := os.Stat(cachePath); err == nil && !info.ModTime().Before(newest) {
		if data, err := os.ReadFile(cachePath); err == nil {
			if ct, ok := comiccover.ContentType(data); ok {
				s.writeCover(w, ct, data)
				return
			}
		}
	}

	for _, f := range comicFiles {
		data, contentType, err := comiccover.Extract(f.Path)
		if err != nil {
			slog.Debug("cover extraction failed", "bookId", id, "path", f.Path, "error", err)
			continue
		}
		s.cacheCover(cachePath, data)
		s.writeCover(w, contentType, data)
		return
	}
	writeError(w, http.StatusNotFound, "no cover available")
}

// handleImage proxies a provider image (author/series/book art) through the
// on-disk cache: served from cache when present, downloaded and cached on
// first request. On any failure it redirects to the origin URL so the image
// still loads. ?url= is the provider URL.
func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	data, contentType, err := s.images.Fetch(ctx, url)
	if err != nil {
		// Fall back to the origin only for real web URLs — never reflect an
		// arbitrary scheme into a Location header.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			writeError(w, http.StatusBadRequest, "url must be http(s)")
			return
		}
		slog.Debug("image proxy fetch failed, redirecting to origin", "url", url, "error", err)
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Write(data)
}

// prefetchAuthorImages downloads an author's portrait and their books' cover
// art into the image cache in the background (download-on-add/refresh).
func (s *server) prefetchAuthorImages(id int64) {
	if a, err := s.store.GetAuthor(id); err == nil {
		s.images.Prefetch(a.ImageURL)
	}
	if books, err := s.store.ListBooks(id); err == nil {
		for i := range books {
			s.images.Prefetch(books[i].CoverURL)
		}
	}
}

// prefetchSeriesImages downloads a series' cover and its volumes'/issues'
// covers into the image cache in the background.
func (s *server) prefetchSeriesImages(id int64) {
	if sr, err := s.store.GetSeries(id); err == nil {
		s.images.Prefetch(sr.CoverURL)
	}
	if vols, err := s.store.ListVolumes(id); err == nil {
		for i := range vols {
			s.images.Prefetch(vols[i].CoverURL)
		}
	}
}

// clearExtractedCovers deletes the covers extracted from owned comic archives
// (`<data>/covers/book-*`), leaving provider art (covers/remote) alone. They
// re-extract from the files on the next request.
func (s *server) clearExtractedCovers() (removed int, freed int64, err error) {
	dir := filepath.Join(s.cfg.DataDir(), "covers")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "book-") {
			continue
		}
		if info, err := e.Info(); err == nil {
			freed += info.Size()
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed, freed, nil
}

// handleClearCoverCache clears the extracted comic covers.
func (s *server) handleClearCoverCache(w http.ResponseWriter, r *http.Request) {
	removed, freed, err := s.clearExtractedCovers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "freedBytes": freed})
}

// handleClearDescriptions blanks stored descriptions; they're re-fetched on
// the next metadata refresh.
func (s *server) handleClearDescriptions(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.ClearDescriptions()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"descriptionsCleared": n})
}

// handleClearAllCache clears every rebuildable cache at once: provider art,
// extracted comic covers, and stored descriptions.
func (s *server) handleClearAllCache(w http.ResponseWriter, r *http.Request) {
	removedArt, freedArt, err := s.images.Clear()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	removedCovers, freedCovers, err := s.clearExtractedCovers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	descN, err := s.store.ClearDescriptions()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":             removedArt + removedCovers,
		"freedBytes":          freedArt + freedCovers,
		"descriptionsCleared": descN,
	})
}

func (s *server) writeCover(w http.ResponseWriter, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

// cacheCover writes the cover atomically (temp file + rename) so concurrent
// requests never read a partial file. Failures are non-fatal — the cover is
// still served, just re-extracted next time.
func (s *server) cacheCover(cachePath string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		slog.Debug("cover cache mkdir failed", "error", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".cover-*")
	if err != nil {
		slog.Debug("cover cache temp failed", "error", err)
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		os.Remove(tmp.Name())
	}
}

func (s *server) writeBookDetail(w http.ResponseWriter, status int, id int64) {
	book, err := s.store.GetBook(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if book.Editions, err = s.store.ListEditions(id); err != nil {
		writeStoreError(w, err)
		return
	}
	if book.Series, err = s.store.ListSeriesForBook(id); err != nil {
		writeStoreError(w, err)
		return
	}
	if book.Files, err = s.store.ListBookFiles(id); err != nil {
		writeStoreError(w, err)
		return
	}
	fillAudioTracks(book.Files)
	writeJSON(w, status, book)
}

// fillAudioTracks lists the tracks of multi-file audiobook units (records
// whose path is the book folder) so the UI can show every file, not just the
// folder. Best-effort: an unreadable folder simply carries no track list.
func fillAudioTracks(files []library.BookFile) {
	for i := range files {
		f := &files[i]
		if f.MediaType != "audiobook" {
			continue
		}
		info, err := os.Stat(f.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		var tracks []library.Track
		_ = filepath.WalkDir(f.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !scanner.IsAudioPath(p) {
				return nil
			}
			rel, err := filepath.Rel(f.Path, p)
			if err != nil {
				rel = filepath.Base(p)
			}
			var size int64
			if fi, err := d.Info(); err == nil {
				size = fi.Size()
			}
			tracks = append(tracks, library.Track{Name: filepath.ToSlash(rel), Size: size})
			return nil
		})
		slices.SortFunc(tracks, func(a, b library.Track) int {
			return strings.Compare(a.Name, b.Name)
		})
		f.Tracks = tracks
	}
}

func (s *server) handleMonitorBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Monitored bool `json:"monitored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetBookMonitored(id, req.Monitored); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "monitored": req.Monitored})
}

func (s *server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)
	var paths []string
	if deleteFiles {
		var err error
		if paths, err = s.store.FilePathsForBook(id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := s.store.DeleteBook(id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.finishDelete(w, deleteFiles, paths)
}
