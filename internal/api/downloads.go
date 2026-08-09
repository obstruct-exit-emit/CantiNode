package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/scanner"
)

// downloadTimeout bounds a grab/import request. It's generous because a debrid
// bridge accepts a magnet synchronously (waiting on the debrid service), which
// can take over a minute; a tighter bound would abandon adds that then land
// unrecorded.
const downloadTimeout = 150 * time.Second

func writeDownloadError(w http.ResponseWriter, err error) {
	if errors.Is(err, download.ErrNotFound) {
		writeError(w, http.StatusNotFound, "download client not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// decodeDownloadClient reads and validates a client config from the body.
func decodeDownloadClient(r *http.Request) (*download.ClientConfig, string) {
	var c download.ClientConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		return nil, "invalid JSON body"
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Host = strings.TrimRight(strings.TrimSpace(c.Host), "/")
	if c.Name == "" {
		return nil, "name is required"
	}
	switch c.Type {
	case download.TypeQBittorrent, download.TypeSABnzbd:
		if !strings.HasPrefix(c.Host, "http://") && !strings.HasPrefix(c.Host, "https://") {
			return nil, "host must be an http(s) URL"
		}
	case download.TypeDirect:
		// The direct client is LibriNode's own fetcher: its "host" is the
		// local folder downloads land in, not a URL.
		if c.Host == "" {
			return nil, "a download folder is required"
		}
	default:
		return nil, "type must be qbittorrent, sabnzbd, or direct"
	}
	// A SABnzbd API key is optional: SABnzbd-compatible endpoints such as
	// Real-Debrid's (which downloads NZBs behind a fake-SABnzbd interface)
	// need no key. Real SABnzbd will reject unauthenticated calls, which the
	// connection Test surfaces — so we let it be entered without one.
	if c.Category == "" {
		c.Category = "librinode"
	}
	if c.Priority <= 0 || c.Priority > 50 {
		c.Priority = 1
	}
	return &c, ""
}

func (s *server) handleListDownloadClients(w http.ResponseWriter, r *http.Request) {
	configs, err := s.downloads.Store().List()
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	// Prowlarr reads download clients during app sync to learn which release
	// protocols the app handles (it only syncs torrent indexers to an app
	// with a torrent client). Serve it the Readarr shape with `protocol`; the
	// browser UI keeps its native shape.
	if isProwlarr(r) {
		out := make([]map[string]any, 0, len(configs))
		for _, c := range configs {
			out = append(out, readarrDownloadClient(c))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *server) handleAddDownloadClient(w http.ResponseWriter, r *http.Request) {
	c, msg := decodeDownloadClient(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.downloads.Store().Add(c); err != nil {
		writeError(w, http.StatusConflict, "could not save client (duplicate name?): "+err.Error())
		return
	}
	s.downloads.InvalidateQueue()
	s.refreshHealth()
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) handleUpdateDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, msg := decodeDownloadClient(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	c.ID = id
	if err := s.downloads.Store().Update(c); err != nil {
		writeDownloadError(w, err)
		return
	}
	updated, err := s.downloads.Store().Get(id)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	s.downloads.InvalidateQueue()
	s.refreshHealth()
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) handleDeleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.downloads.Store().Delete(id); err != nil {
		writeDownloadError(w, err)
		return
	}
	s.downloads.InvalidateQueue()
	s.refreshHealth()
	w.WriteHeader(http.StatusNoContent)
}

// handleTestDownloadClient checks an unsaved client config against the live
// service.
func (s *server) handleTestDownloadClient(w http.ResponseWriter, r *http.Request) {
	c, msg := decodeDownloadClient(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	client, err := download.New(c)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()

	if err := client.Test(ctx); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGrabRelease sends a release to the matching download client — the
// button behind interactive search results. bookId ties the download to a
// book so Completed Download Handling can import it automatically.
func (s *server) handleGrabRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		DownloadURL string `json:"downloadUrl"`
		GUID        string `json:"guid"`
		Protocol    string `json:"protocol"`
		BookID      int64  `json:"bookId"`
		MediaType   string `json:"mediaType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DownloadURL == "" {
		writeError(w, http.StatusBadRequest,
			"this release has no download URL — the source may require a membership/API key for downloads")
		return
	}
	switch req.Protocol {
	case download.ProtocolTorrent, download.ProtocolUsenet, download.ProtocolDirect:
	default:
		writeError(w, http.StatusBadRequest, "protocol must be torrent, usenet, or direct")
		return
	}
	if req.MediaType == "" {
		req.MediaType = "ebook"
	}
	if req.BookID > 0 {
		if _, err := s.store.GetBook(req.BookID); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()

	result, grab, err := s.downloads.GrabRelease(ctx, req.Protocol, req.DownloadURL, req.Title, req.GUID, req.BookID, req.MediaType)
	if errors.Is(err, download.ErrNoClient) {
		writeError(w, http.StatusServiceUnavailable,
			"no enabled "+req.Protocol+" download client — add one under Settings")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client": result.Client, "id": result.ID, "grabId": grab.ID,
	})
}

// handleAutoSearchBook searches indexers for one book and grabs the best
// approved release automatically.
func (s *server) handleAutoSearchBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	outcome, err := s.search.SearchBook(ctx, id, r.URL.Query().Get("mediaType"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, outcome)
}

// handleSearchWanted runs the automatic search over every wanted book now
// (the background loop does the same on a schedule).
func (s *server) handleSearchWanted(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	outcomes, err := s.search.SearchWanted(ctx)
	if err != nil && len(outcomes) == 0 {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	grabbed := 0
	for _, o := range outcomes {
		if o.Grabbed {
			grabbed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"searched": len(outcomes), "grabbed": grabbed, "outcomes": outcomes,
	})
}

// handleImport runs one Completed Download Handling pass on demand.
func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()

	result, err := s.importer.Run(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleHistory lists grab records, newest first, paged:
// GET /history?search=&limit=&offset= → {"records": […], "total": N}.
func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	records, total, err := s.downloads.Store().GrabHistory(q.Get("search"), limit, offset)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records, "total": total})
}

// handleCancelGrab manually resolves a pending grab as failed, without
// touching any download client. A grab can be left permanently "pending" —
// blocking any new search or grab for its book — when its queue entry is
// already gone: removed directly in the client, lost to a client restart, or
// (before this fix) a torrent grab whose client item id was never reliably
// recorded. This is the manual escape hatch for a grab already stuck that
// way, independent of whatever state the client itself is in.
func (s *server) handleCancelGrab(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid grab id")
		return
	}
	if err := s.downloads.Store().ResolveGrab(id, download.GrabStatusFailed, "manually cancelled"); err != nil {
		if errors.Is(err, download.ErrNotFound) {
			writeError(w, http.StatusNotFound, "grab not found")
			return
		}
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": id})
}

// handleBlocklist lists releases blocked after failed downloads.
func (s *server) handleBlocklist(w http.ResponseWriter, r *http.Request) {
	entries, err := s.downloads.Store().ListBlocklist()
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleUnblock removes one blocklist entry so the release can be grabbed
// again.
func (s *server) handleUnblock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.downloads.Store().DeleteBlock(id); err != nil {
		writeDownloadError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleQueue shows every LibriNode download across all enabled clients.
// queueItem is a download client item enriched with the pending grab it
// belongs to, so the UI can link a queue line to its book and show a book's
// download progress on its own page.
type queueItem struct {
	download.Item
	GrabID    int64  `json:"grabId,omitempty"`
	BookID    int64  `json:"bookId,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

// enrichQueue pairs client items with pending grabs — by client item id
// first, by normalized title otherwise — mirroring the importer's matching.
// The title map covers every pending grab, not just ones with no client item
// id: a torrent grab's id can still be wrong (a same-titled item already in
// the client at grab time, or a record from before the client-item-id fix
// existed), so title stays a fallback even then instead of leaving that grab
// unlinked.
func (s *server) enrichQueue(items []download.Item) []queueItem {
	out := make([]queueItem, len(items))
	for i := range items {
		out[i] = queueItem{Item: items[i]}
	}
	pending, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		return out
	}
	byID := map[string]*download.GrabRecord{}
	byTitle := map[string]*download.GrabRecord{}
	for i := range pending {
		g := &pending[i]
		if g.ClientItemID != "" {
			byID[g.ClientItemID] = g
		}
		byTitle[scanner.Normalize(g.Title)] = g
	}
	for i := range out {
		g := byID[out[i].ID]
		if g == nil {
			g = byTitle[scanner.Normalize(out[i].Title)]
		}
		if g != nil {
			out[i].GrabID = g.ID
			out[i].BookID = g.BookID
			out[i].MediaType = g.MediaType
		}
	}
	return out
}

func (s *server) handleQueue(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()

	items, errs, err := s.downloads.Queue(ctx)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.enrichQueue(items), "errors": errs})
}

// handleRemoveQueueItem removes one download from its client (with its data)
// and resolves the matching pending grab as failed — without blocklisting, so
// the release stays grabbable if the user wants it again.
func (s *server) handleRemoveQueueItem(w http.ResponseWriter, r *http.Request) {
	configID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid client id")
		return
	}
	itemID := r.PathValue("itemId")
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "item id is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()

	if err := s.downloads.Remove(ctx, configID, itemID, true); err != nil {
		writeDownloadError(w, err)
		return
	}
	// Resolve the pending grab this item belonged to (best-effort). The UI
	// passes the grab id it got from queue enrichment — the only reliable link
	// for torrents, whose grabs carry no client item id (qBittorrent's add
	// returns none). The item-id match remains as a fallback.
	if pending, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed); err == nil {
		grabID, _ := strconv.ParseInt(r.URL.Query().Get("grabId"), 10, 64)
		for i := range pending {
			g := &pending[i]
			if g.ID == grabID ||
				(g.ClientItemID != "" && g.ClientItemID == itemID && g.ClientConfigID == configID) {
				_ = s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed, "removed from queue")
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": itemID})
}
