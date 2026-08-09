// Package refresh syncs library records with the metadata provider: adding
// authors/books pulls them in, manual refresh re-fetches them, and a periodic
// loop keeps the whole library current. Store upserts preserve user-owned
// monitored flags, so refreshing never undoes monitoring choices.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

type Service struct {
	store     *library.Store
	providers *metadata.Manager
}

// unreachableStreak aborts a refresh sweep after several consecutive
// "provider didn't respond" outcomes in a row, instead of letting a mid-sweep
// outage turn into every remaining record timing out one at a time — a
// library of a few hundred authors could otherwise turn a brief Hardcover
// outage into an hours-long stuck refresh. Any other outcome (success, or an
// error that isn't about connectivity) resets the streak, since that means
// the provider IS answering.
type unreachableStreak struct{ n int }

const unreachableAbortThreshold = 3

// hit records one call's outcome and reports whether the streak just crossed
// the abort threshold — the caller should stop the sweep.
func (u *unreachableStreak) hit(err error) bool {
	if err != nil && errors.Is(err, metadata.ErrUnreachable) {
		u.n++
		return u.n >= unreachableAbortThreshold
	}
	u.n = 0
	return false
}

func New(store *library.Store, providers *metadata.Manager) *Service {
	return &Service{store: store, providers: providers}
}

// provider returns the active metadata provider, or ErrNotConfigured. Looked
// up per call so a provider configured in the settings UI takes effect
// immediately.
func (s *Service) provider() (metadata.Provider, error) {
	if p := s.providers.Current(); p != nil {
		return p, nil
	}
	return nil, metadata.ErrNotConfigured
}

// SyncAuthor fetches an author and their full bibliography from the active
// provider and persists everything. Books are stored as metadata only —
// library membership is never touched here, so refreshes can't enroll,
// un-enroll, or re-monitor anything; new books surface in the author's
// Missing section.
func (s *Service) SyncAuthor(ctx context.Context, foreignID string, monitored bool) (*library.Author, error) {
	p, err := s.provider()
	if err != nil {
		return nil, err
	}
	return s.syncAuthorWith(ctx, p, foreignID, monitored, 0)
}

// syncAuthorWith is SyncAuthor through an explicit provider — the caller
// resolves it (the active provider on add, or the author's provider override
// on refresh). existingID pins the author to an already-known row (a
// refresh, so a provider-override switch — which changes source and foreign
// id — updates that row instead of creating a second one); 0 lets
// UpsertAuthor create-or-update by the natural (metadata_source, foreign_id)
// key, as a fresh add does.
func (s *Service) syncAuthorWith(ctx context.Context, p metadata.Provider, foreignID string, monitored bool, existingID int64) (*library.Author, error) {
	remote, err := p.GetAuthor(ctx, foreignID)
	if err != nil {
		return nil, err
	}

	// A fallback-sourced author stamps its origin; its books inherit it unless
	// they carry their own. See metadata.FallbackProvider.
	source := remote.Source
	if source == "" {
		source = p.Name()
	}
	author := &library.Author{
		ID:          existingID,
		Source:      source,
		ForeignID:   remote.ForeignID,
		Name:        remote.Name,
		Description: remote.Description,
		ImageURL:    remote.ImageURL,
		Monitored:   monitored,
	}
	if err := s.store.UpsertAuthor(author); err != nil {
		return nil, err
	}

	// A provider-override refresh's bibliography carries a fresh set of
	// foreign ids that won't match any book already on file under the old
	// provider's — match by title instead, so a book the user already owns or
	// enrolled updates in place rather than getting a duplicate row alongside
	// an orphaned original under the old id.
	byTitle := map[string]int64{}
	if existingID != 0 {
		if existingBooks, err := s.store.ListBooks(author.ID); err == nil {
			for _, b := range existingBooks {
				byTitle[strings.ToLower(strings.TrimSpace(b.Title))] = b.ID
			}
		}
	}
	for i := range remote.Books {
		if remote.Books[i].Source == "" {
			remote.Books[i].Source = source
		}
		knownBookID := byTitle[strings.ToLower(strings.TrimSpace(remote.Books[i].Title))]
		if err := s.persistBook(p, &remote.Books[i], author.ID, monitored, knownBookID); err != nil {
			return nil, err
		}
	}

	// Reconcile: drop bibliography entries the provider no longer returns — e.g.
	// foreign-language editions or box sets it now filters out — so a refresh
	// clears stale junk from the author's "missing" list instead of accumulating
	// it forever (the sync only ever upserts). Only books the user never enrolled
	// in a library and owns no file for are removed; anything owned or added is
	// always kept. Guarded by a non-empty fresh bibliography so a provider hiccup
	// returning nothing can never wipe the shelf.
	if len(remote.Books) > 0 {
		fresh := make(map[string]bool, len(remote.Books))
		for i := range remote.Books {
			fresh[remote.Books[i].ForeignID] = true
		}
		if existing, err := s.store.ListBooks(author.ID); err == nil {
			for _, b := range existing {
				if fresh[b.ForeignID] || b.InEbookLibrary || b.InAudiobookLibrary || b.HasFile {
					continue
				}
				if err := s.store.DeleteBook(b.ID); err != nil {
					slog.Warn("reconcile: removing stale bibliography book",
						"book", b.Title, "author", author.Name, "error", err)
				}
			}
		}
	}
	return author, nil
}

// SyncBook fetches one book (with editions and series) from the provider and
// persists it. The author is created as an unmonitored stub when not in the
// library yet — adding a single book must not pull in the whole bibliography.
// Library membership is the caller's job (handleAddBook enrolls explicitly).
func (s *Service) SyncBook(ctx context.Context, foreignID string, monitored bool) (*library.Book, error) {
	p, err := s.provider()
	if err != nil {
		return nil, err
	}
	return s.syncBookWith(ctx, p, foreignID, monitored, 0, 0)
}

// syncBookWith is SyncBook through an explicit provider — the caller
// resolves it (the active provider on add, or the author's provider override
// on refresh). knownAuthorID pins the book to an author the caller already
// resolved (a refresh, so a provider-override switch — which changes the
// remote author's foreign id — reuses the existing author instead of
// creating a duplicate stub under the new provider's id); 0 derives/creates
// one from the remote book's author info, as a fresh add does. existingID
// likewise pins the book itself to an already-known row; 0 lets UpsertBook
// create-or-update by natural key.
func (s *Service) syncBookWith(ctx context.Context, p metadata.Provider, foreignID string, monitored bool, knownAuthorID, existingID int64) (*library.Book, error) {
	remote, err := p.GetBook(ctx, foreignID)
	if err != nil {
		return nil, err
	}
	if knownAuthorID == 0 && remote.AuthorForeignID == "" {
		return nil, fmt.Errorf("provider returned book %s without an author", foreignID)
	}

	// A fallback-sourced book stamps its origin; the author stub it creates
	// and the read-back share that source. See metadata.FallbackProvider.
	source := remote.Source
	if source == "" {
		source = p.Name()
	}

	authorID := knownAuthorID
	if authorID == 0 {
		author, err := s.store.GetAuthorByForeignID(source, remote.AuthorForeignID)
		if errors.Is(err, library.ErrNotFound) {
			author = &library.Author{
				Source:    source,
				ForeignID: remote.AuthorForeignID,
				Name:      remote.AuthorName,
				Monitored: false,
			}
			err = s.store.UpsertAuthor(author)
		}
		if err != nil {
			return nil, err
		}
		authorID = author.ID
	}

	if err := s.persistBook(p, remote, authorID, monitored, existingID); err != nil {
		return nil, err
	}
	if existingID != 0 {
		return s.store.GetBook(existingID)
	}
	return s.store.GetBookByForeignID(source, remote.ForeignID)
}

// bookProviderFor resolves the provider for an author-scoped refresh: the
// author's provider override wins when set; otherwise the active provider —
// which is nil when Settings → Metadata says None, and libraries always
// honor the settings.
func (s *Service) bookProviderFor(author *library.Author) (metadata.Provider, error) {
	if author.ProviderOverride != "" {
		if p := s.providers.ProviderByName(author.ProviderOverride); p != nil {
			return p, nil
		}
		return nil, metadata.ErrNotConfigured
	}
	return s.provider()
}

// resolveAuthorForeignID returns the id to fetch author with from p. When p
// is the author's own source, that's just the stored ForeignID (the normal
// case). A provider override names a DIFFERENT provider — the stored id
// belongs to the original source's namespace and means nothing there (an
// Open Library key doesn't exist in Hardcover's ids, or vice versa), so it
// has to be found again by name before it can be fetched. Prefers an exact
// (case-insensitive) name match; falls back to the first result rather than
// failing outright when the provider's own casing/punctuation differs
// slightly (initials, diacritics).
func (s *Service) resolveAuthorForeignID(ctx context.Context, p metadata.Provider, author *library.Author) (string, error) {
	if author.Source == "" || author.Source == p.Name() {
		return author.ForeignID, nil
	}
	candidates, err := p.SearchAuthors(ctx, author.Name)
	if err != nil {
		return "", err
	}
	for _, c := range candidates {
		if strings.EqualFold(c.Name, author.Name) {
			return c.ForeignID, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0].ForeignID, nil
	}
	return "", fmt.Errorf("no author named %q found on %s: %w", author.Name, p.Name(), metadata.ErrNotFound)
}

// resolveBookForeignID is RefreshBook's equivalent of resolveAuthorForeignID:
// a provider override means the book's stored id belongs to a different
// provider's namespace, so it has to be found again by title first. Prefers
// a result whose title AND author both match; falls back to any title
// match, then the first result — title alone is common enough (series
// entries, reprints) that author agreement matters when it's available.
func (s *Service) resolveBookForeignID(ctx context.Context, p metadata.Provider, book *library.Book, authorName string) (string, error) {
	if book.Source == "" || book.Source == p.Name() {
		return book.ForeignID, nil
	}
	candidates, err := p.SearchBooks(ctx, book.Title)
	if err != nil {
		return "", err
	}
	titleMatch := ""
	for _, c := range candidates {
		if !strings.EqualFold(c.Title, book.Title) {
			continue
		}
		if titleMatch == "" {
			titleMatch = c.ForeignID
		}
		if authorName != "" && strings.EqualFold(c.AuthorName, authorName) {
			return c.ForeignID, nil
		}
	}
	if titleMatch != "" {
		return titleMatch, nil
	}
	if len(candidates) > 0 {
		return candidates[0].ForeignID, nil
	}
	return "", fmt.Errorf("no book titled %q found on %s: %w", book.Title, p.Name(), metadata.ErrNotFound)
}

// RefreshAuthor re-syncs an existing author by local id. Books discovered
// since the last sync are added with the author's monitored flag.
func (s *Service) RefreshAuthor(ctx context.Context, id int64) error {
	author, err := s.store.GetAuthor(id)
	if err != nil {
		return err
	}
	p, err := s.bookProviderFor(author)
	if err != nil {
		return err
	}
	foreignID, err := s.resolveAuthorForeignID(ctx, p, author)
	if err != nil {
		return err
	}
	_, err = s.syncAuthorWith(ctx, p, foreignID, author.Monitored, author.ID)
	return err
}

// RefreshBook re-syncs an existing book by local id, updating its metadata,
// series links, and editions. It follows the book's author's provider
// override.
func (s *Service) RefreshBook(ctx context.Context, id int64) error {
	book, err := s.store.GetBook(id)
	if err != nil {
		return err
	}
	author, err := s.store.GetAuthor(book.AuthorID)
	if err != nil {
		return err
	}
	p, err := s.bookProviderFor(author)
	if err != nil {
		return err
	}
	foreignID, err := s.resolveBookForeignID(ctx, p, book, author.Name)
	if err != nil {
		return err
	}
	_, err = s.syncBookWith(ctx, p, foreignID, book.Monitored, author.ID, book.ID)
	return err
}

// persistBook stores a provider book plus its series links and editions
// under the given author. Library membership columns are left at their
// defaults (new books) or preserved (existing books) — enrollment is always
// an explicit user action. (New ebook editions still inherit the book's
// monitored flag into the legacy editions.monitored column.) existingID pins
// the book to an already-known row (a refresh); 0 lets UpsertBook
// create-or-update by natural key, as a fresh add does.
func (s *Service) persistBook(p metadata.Provider, remote *metadata.Book, authorID int64, monitored bool, existingID int64) error {
	// A fallback-sourced record stamps its true origin; otherwise the provider
	// that returned it is the source. See metadata.FallbackProvider.
	source := remote.Source
	if source == "" {
		source = p.Name()
	}
	book := &library.Book{
		ID:          existingID,
		AuthorID:    authorID,
		Source:      source,
		ForeignID:   remote.ForeignID,
		Title:       remote.Title,
		Description: remote.Description,
		ReleaseDate: remote.ReleaseDate,
		Rating:      remote.Rating,
		CoverURL:    remote.CoverURL,
		Monitored:   monitored,
	}
	if err := s.store.UpsertBook(book); err != nil {
		return err
	}
	keepSeries := make([]int64, 0, len(remote.Series))
	for _, sl := range remote.Series {
		series := &library.Series{
			Source:      source,
			ForeignID:   sl.ForeignID,
			Title:       sl.Title,
			Description: sl.Description,
		}
		if err := s.store.UpsertSeries(series); err != nil {
			return err
		}
		if err := s.store.LinkBookSeries(book.ID, series.ID, sl.Position); err != nil {
			return err
		}
		keepSeries = append(keepSeries, series.ID)
	}
	// Reconcile: drop any stale series link the provider no longer reports, so
	// a refresh heals a wrong link (e.g. a standalone once mislabeled as part of
	// a series) instead of leaving it to corrupt the organized file path.
	if err := s.store.SetBookSeries(book.ID, keepSeries); err != nil {
		return err
	}
	for _, ed := range remote.Editions {
		edition := &library.Edition{
			BookID:      book.ID,
			Source:      source,
			ForeignID:   ed.ForeignID,
			Title:       ed.Title,
			ISBN13:      ed.ISBN13,
			ASIN:        ed.ASIN,
			Format:      ed.Format,
			Publisher:   ed.Publisher,
			Language:    ed.Language,
			ReleaseDate: ed.ReleaseDate,
			CoverURL:    ed.CoverURL,
			Monitored:   monitored && ed.Format == library.FormatEbook,
		}
		if err := s.store.UpsertEdition(edition); err != nil {
			return err
		}
	}
	return nil
}

// RefreshAll re-syncs every author and manga/comic series in the library.
// Individual failures are logged and skipped so one dead provider record
// can't stall the rest.
// RefreshLibrary re-syncs one library's records from their providers: a
// format library's member authors (ebook/audiobook) or a series library's
// series (manga/comic) — the library-wide twin of the per-author/per-series
// Refresh buttons, honoring per-record provider overrides the same way.
// Individual failures are logged and skipped; the count of successfully
// refreshed records is returned.
func (s *Service) RefreshLibrary(ctx context.Context, mediaType string) (int, error) {
	done := 0
	switch mediaType {
	case "ebook", "audiobook":
		authors, err := s.store.ListAuthors()
		if err != nil {
			return 0, err
		}
		var unreachable unreachableStreak
		for i := range authors {
			a := &authors[i]
			if mediaType == "ebook" && !a.InEbookLibrary {
				continue
			}
			if mediaType == "audiobook" && !a.InAudiobookLibrary {
				continue
			}
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			err := s.RefreshAuthor(ctx, a.ID)
			if err != nil {
				slog.Warn("library refresh: author failed", "author", a.Name, "error", err)
			}
			if unreachable.hit(err) {
				slog.Warn("library refresh: provider unreachable, aborting the rest of this sweep",
					"mediaType", mediaType)
				return done, nil
			}
			if err == nil {
				done++
			}
		}
	case "manga", "comic":
		seriesList, err := s.store.ListSeries(mediaType)
		if err != nil {
			return 0, err
		}
		var unreachable unreachableStreak
		for i := range seriesList {
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			err := s.RefreshSeries(ctx, seriesList[i].ID)
			if err != nil {
				slog.Warn("library refresh: series failed", "series", seriesList[i].Title, "error", err)
			}
			if unreachable.hit(err) {
				slog.Warn("library refresh: provider unreachable, aborting the rest of this sweep",
					"mediaType", mediaType)
				return done, nil
			}
			if err == nil {
				done++
			}
		}
	default:
		return 0, fmt.Errorf("metadata refresh is not available for %s", mediaType)
	}
	slog.Info("library metadata refresh complete", "mediaType", mediaType, "refreshed", done)
	return done, nil
}

func (s *Service) RefreshAll(ctx context.Context) {
	s.refreshAllSeries(ctx)
	if _, err := s.provider(); err != nil {
		return
	}
	authors, err := s.store.ListAuthors()
	if err != nil {
		slog.Error("metadata refresh: listing authors", "error", err)
		return
	}
	bookProvider, _ := s.provider()
	var unreachable unreachableStreak
	for _, a := range authors {
		if ctx.Err() != nil {
			return
		}
		// Creator stubs from series providers aren't the book provider's to
		// refresh.
		if a.Source != bookProvider.Name() {
			continue
		}
		_, err := s.SyncAuthor(ctx, a.ForeignID, a.Monitored)
		if err != nil {
			slog.Warn("metadata refresh failed", "author", a.Name, "error", err)
		}
		if unreachable.hit(err) {
			slog.Warn("metadata refresh: provider unreachable, aborting the rest of this sweep",
				"provider", bookProvider.Name())
			return
		}
	}
	if len(authors) > 0 {
		slog.Info("metadata refresh complete", "authors", len(authors))
	}
}

// RunPeriodic refreshes the whole library on the given interval until ctx is
// cancelled. The first run happens after one interval, not at startup.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RefreshAll(ctx)
		}
	}
}
