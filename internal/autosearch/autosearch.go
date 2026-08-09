// Package autosearch closes the *arr loop: it finds monitored books without
// files, searches the indexers, and grabs the best-scoring approved release —
// on a schedule, for the whole wanted list, or on demand for one book.
// Completed Download Handling then imports whatever the client finishes.
package autosearch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/release"
)

// WantedSearchPacing is the gap left between consecutive book searches when
// sweeping a wanted list — library-wide or for one author. It keeps a long
// list from firing a rapid burst that trips indexers (or Prowlarr) into a
// rate-limit backoff and leaves them "not responding" until a restart.
const WantedSearchPacing = 500 * time.Millisecond

type Service struct {
	store     *library.Store
	indexers  *indexer.Service
	downloads *download.Service
}

func New(store *library.Store, indexers *indexer.Service, downloads *download.Service) *Service {
	return &Service{store: store, indexers: indexers, downloads: downloads}
}

// BookOutcome reports what one book's automatic search did.
type BookOutcome struct {
	BookID    int64  `json:"bookId"`
	BookTitle string `json:"bookTitle"`
	MediaType string `json:"mediaType"`
	Grabbed   bool   `json:"grabbed"`
	Release   string `json:"release,omitempty"`
	Client    string `json:"client,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SearchBook searches for one book as the given media type and grabs the
// best approved release. Never returns an error for "nothing found" —
// that's an outcome. When the book is already owned in that format and its
// profile allows upgrades, the search runs in upgrade mode: only strictly
// better formats approve.
func (s *Service) SearchBook(ctx context.Context, bookID int64, mediaType string) (*BookOutcome, error) {
	if mediaType == "" {
		mediaType = "ebook"
	}
	book, err := s.store.GetBook(bookID)
	if err != nil {
		return nil, err
	}
	// Volumes dictate their own media type.
	if book.MediaType == "comic" {
		mediaType = book.MediaType
	}
	// Prose searches require library membership — the Plex-style model:
	// a book is only acquirable as a format it was added as.
	if book.MediaType == "book" {
		member := book.InEbookLibrary
		if mediaType == "audiobook" {
			member = book.InAudiobookLibrary
		}
		if !member {
			return &BookOutcome{BookID: bookID, BookTitle: book.Title, MediaType: mediaType,
				Message: "not in the " + mediaType + " library — add it there first"}, nil
		}
	}

	if pending, err := s.pendingBookIDs(); err != nil {
		return nil, err
	} else if pending[pendingKey(bookID, mediaType)] {
		return &BookOutcome{BookID: bookID, BookTitle: book.Title, MediaType: mediaType,
			Message: "a grab is already pending for this book"}, nil
	}

	minScore := 0
	if s.ownedIn(book, mediaType) {
		min, upgradable := s.upgradeMinScore(book, mediaType)
		if !upgradable {
			return &BookOutcome{BookID: bookID, BookTitle: book.Title, MediaType: mediaType,
				Message: "already owned — enable upgrades in the quality profile to search for better formats"}, nil
		}
		minScore = min
	}
	return s.searchOne(ctx, book, mediaType, minScore)
}

// ownedIn reports whether the book has a file of the given media type.
func (s *Service) ownedIn(book *library.Book, mediaType string) bool {
	switch mediaType {
	case "ebook":
		return book.HasEbookFile
	case "audiobook":
		return book.HasAudiobookFile
	}
	return book.HasFile
}

// upgradeMinScore computes upgrade mode for an owned book: the owned
// format's score (candidates must beat it), and whether an upgrade is
// wanted at all (profile allows upgrades and the owned format is below the
// cutoff — empty cutoff means the profile's best format).
func (s *Service) upgradeMinScore(book *library.Book, mediaType string) (int, bool) {
	profile, err := s.store.DefaultProfile(mediaType)
	if err != nil || !profile.UpgradesAllowed || len(profile.Formats) == 0 {
		return 0, false
	}
	prefs := release.PreferencesFromProfile(*profile)

	cutoff := profile.Cutoff
	if cutoff == "" {
		cutoff = profile.Formats[0]
	}
	cutoffScore := prefs.FormatScores[cutoff]

	files, err := s.store.ListBookFiles(book.ID)
	if err != nil {
		return 0, false
	}
	owned := 0
	seen := false
	for _, f := range files {
		if f.MediaType != mediaType {
			continue
		}
		seen = true
		if sc, ok := prefs.FormatScores[f.Format]; ok && sc > owned {
			owned = sc
		}
	}
	if !seen {
		return 0, false
	}
	if owned == 0 {
		// The owned format isn't in the profile at all — anything listed
		// is an upgrade.
		return 1, true
	}
	if owned >= cutoffScore {
		return 0, false // cutoff met; done upgrading
	}
	return owned, true
}

func (s *Service) searchOne(ctx context.Context, book *library.Book, mediaType string, minFormatScore int) (*BookOutcome, error) {
	outcome := &BookOutcome{BookID: book.ID, BookTitle: book.Title, MediaType: mediaType}
	prefs := release.PreferencesFor(s.store, mediaType)
	prefs.MinFormatScore = minFormatScore

	var query string
	var nativeQuery string // title-focused query for scraped sources
	var score func(indexer.Release) release.Candidate

	if mediaType == "comic" {
		// Volumes are searched by series title; the volume number filters
		// candidates during scoring.
		links, err := s.store.ListSeriesForBook(book.ID)
		if err != nil {
			return nil, err
		}
		if len(links) == 0 {
			outcome.Message = "volume has no series link"
			return outcome, nil
		}
		seriesTitle, number := links[0].Title, links[0].Position
		query = seriesTitle
		nativeQuery = seriesTitle
		score = func(rel indexer.Release) release.Candidate {
			return release.ScoreVolume(rel, prefs, seriesTitle, number)
		}
	} else {
		author, err := s.store.GetAuthor(book.AuthorID)
		if err != nil {
			return nil, err
		}
		query = author.Name + " " + book.Title
		nativeQuery = book.Title // scraped sources match the bare title, not "author title"
		if mediaType == "audiobook" {
			// Categories do most of the filtering; the keyword helps
			// indexers with sloppy category mapping.
			query += " audiobook"
		}
		otherTitles := otherBookTitles(s.store, book)
		score = func(rel indexer.Release) release.Candidate {
			return release.Score(rel, prefs, book, author, otherTitles)
		}
	}

	found, indexerErrs, err := s.indexers.SearchAll(ctx, query, nativeQuery, mediaType)
	if err != nil {
		return nil, err
	}

	candidates := make([]release.Candidate, 0, len(found))
	for _, rel := range found {
		candidates = append(candidates, score(rel))
	}
	s.markBlocked(candidates)
	release.Rank(candidates)

	var best *release.Candidate
	for i := range candidates {
		if candidates[i].Approved {
			best = &candidates[i]
			break
		}
	}
	if best == nil {
		outcome.Message = fmt.Sprintf("no approved release among %d candidates", len(candidates))
		if len(indexerErrs) > 0 {
			outcome.Message += " (" + strings.Join(indexerErrs, "; ") + ")"
		}
		return outcome, nil
	}

	result, _, err := s.downloads.GrabRelease(ctx, best.Protocol, best.DownloadURL, best.Title, best.GUID, book.ID, mediaType)
	if err != nil {
		outcome.Message = "grab failed: " + err.Error()
		return outcome, nil
	}
	outcome.Grabbed = true
	outcome.Release = best.Title
	outcome.Client = result.Client
	slog.Info("auto-grabbed release",
		"book", book.Title, "mediaType", mediaType, "release", best.Title, "client", result.Client)
	return outcome, nil
}

// PackSearchResult is a series-level pack search: candidates that look like
// whole-series (or multi-volume) releases, plus the wanted volume the grab
// should bind to — the pack importer fills every other matching volume when
// the download completes.
type PackSearchResult struct {
	Candidates []release.Candidate `json:"releases"`
	Errors     []string            `json:"errors"`
	// GrabBookID is the representative missing volume a pack grab is recorded
	// against (the UI passes it to the normal grab endpoint).
	GrabBookID int64 `json:"grabBookId"`
	// Missing is how many of the series' volumes have no file yet.
	Missing int `json:"missing"`
}

// SearchSeriesPacks searches indexers for whole-series packs of a comic
// series — the deliberate answer to the "series torrents are packs" content
// gap. It never auto-grabs: the user picks from the returned list.
func (s *Service) SearchSeriesPacks(ctx context.Context, seriesID int64) (*PackSearchResult, error) {
	series, err := s.store.GetSeries(seriesID)
	if err != nil {
		return nil, err
	}
	if series.MediaType != "comic" {
		return nil, fmt.Errorf("pack search is for comic series (this is %s)", series.MediaType)
	}

	volumes, err := s.store.ListVolumes(seriesID)
	if err != nil {
		return nil, err
	}
	positions, err := s.store.SeriesBookPositions(seriesID)
	if err != nil {
		return nil, err
	}
	result := &PackSearchResult{Candidates: []release.Candidate{}, Errors: []string{}}
	var maxWanted float64
	for i := range volumes {
		v := &volumes[i]
		if v.HasFile {
			continue
		}
		result.Missing++
		if result.GrabBookID == 0 {
			result.GrabBookID = v.ID
		}
		if p := positions[v.ID]; p > maxWanted {
			maxWanted = p
		}
	}
	if result.Missing == 0 {
		return nil, fmt.Errorf("every volume of %s already has a file", series.Title)
	}

	prefs := release.PreferencesFor(s.store, series.MediaType)
	found, indexerErrs, err := s.indexers.SearchAll(ctx, series.Title, series.Title, series.MediaType)
	if err != nil {
		return nil, err
	}
	result.Errors = indexerErrs

	for _, rel := range found {
		result.Candidates = append(result.Candidates, release.ScoreSeriesPack(rel, prefs, series.Title, maxWanted))
	}
	s.markBlocked(result.Candidates)
	release.Rank(result.Candidates)
	return result, nil
}

// markBlocked rejects candidates on the failed-release blocklist.
func (s *Service) markBlocked(candidates []release.Candidate) {
	blocked, err := s.downloads.Store().BlockedKeys()
	if err != nil || len(blocked) == 0 {
		return
	}
	for i := range candidates {
		c := &candidates[i]
		if download.IsBlocked(blocked, c.GUID, c.Title) {
			c.Approved = false
			c.Rejections = append(c.Rejections, "blocklisted (failed to download previously)")
		}
	}
}

func pendingKey(bookID int64, mediaType string) string {
	return fmt.Sprintf("%d/%s", bookID, mediaType)
}

// otherBookTitles returns the author's other prose book titles — release
// scoring's only use for them is flagging a release that names one of them
// alongside the wanted book as a pack (e.g. "Tau Zero & The Boat of a Million
// Years"), bundling two standalone titles without using any "Complete"/
// "Collection"/volume-span wording at all.
func otherBookTitles(store *library.Store, book *library.Book) []string {
	books, err := store.ListBooks(book.AuthorID)
	if err != nil {
		return nil
	}
	titles := make([]string, 0, len(books))
	for _, b := range books {
		if b.MediaType == "book" && b.ID != book.ID {
			titles = append(titles, b.Title)
		}
	}
	return titles
}

// pendingBookIDs is the set of book/media-type pairs that already have an
// unresolved grab — searching those again would double-download.
func (s *Service) pendingBookIDs() (map[string]bool, error) {
	grabs, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		return nil, err
	}
	pending := map[string]bool{}
	for _, g := range grabs {
		if g.BookID > 0 {
			pending[pendingKey(g.BookID, g.MediaType)] = true
		}
	}
	return pending, nil
}

// wants lists the media types a book should be searched as: ebooks whenever
// monitored and fileless; audiobooks additionally require a monitored
// audiobook edition (the per-book opt-in). Owned formats stay wanted in
// upgrade mode (minScore > 0) while the profile allows upgrades and the
// owned format is below the cutoff.
type want struct {
	mediaType string
	minScore  int
}

func (s *Service) wants(book *library.Book) []want {
	// Comic issues want exactly their own type (no upgrade mode for issues
	// yet); the classic monitored flag governs them.
	if book.MediaType == "comic" {
		if !book.Monitored || book.HasFile {
			return nil
		}
		return []want{{mediaType: book.MediaType}}
	}
	// Prose books: each format library membership carries its own monitored
	// flag (Plex-style explicit membership).
	wants := []want{}
	if book.InEbookLibrary && book.EbookMonitored {
		if !book.HasEbookFile {
			wants = append(wants, want{mediaType: "ebook"})
		} else if min, ok := s.upgradeMinScore(book, "ebook"); ok {
			wants = append(wants, want{mediaType: "ebook", minScore: min})
		}
	}
	if book.InAudiobookLibrary && book.AudiobookMonitored {
		if !book.HasAudiobookFile {
			wants = append(wants, want{mediaType: "audiobook"})
		} else if min, ok := s.upgradeMinScore(book, "audiobook"); ok {
			wants = append(wants, want{mediaType: "audiobook", minScore: min})
		}
	}
	return wants
}

// SearchWanted searches every monitored book missing a wanted format,
// politely pacing indexer traffic. Pending grabs are skipped per media type.
func (s *Service) SearchWanted(ctx context.Context) ([]BookOutcome, error) {
	books, err := s.store.ListBooks(0)
	if err != nil {
		return nil, err
	}
	pending, err := s.pendingBookIDs()
	if err != nil {
		return nil, err
	}

	outcomes := []BookOutcome{}

	for i := range books {
		book := &books[i]
		for _, w := range s.wants(book) {
			if pending[pendingKey(book.ID, w.mediaType)] {
				continue
			}
			if ctx.Err() != nil {
				return outcomes, ctx.Err()
			}
			outcome, err := s.searchOne(ctx, book, w.mediaType, w.minScore)
			if err != nil {
				outcomes = append(outcomes, BookOutcome{
					BookID: book.ID, BookTitle: book.Title, MediaType: w.mediaType, Message: err.Error(),
				})
				continue
			}
			outcomes = append(outcomes, *outcome)

			// Pace between searches so a big wanted list doesn't hammer indexers.
			select {
			case <-ctx.Done():
				return outcomes, ctx.Err()
			case <-time.After(WantedSearchPacing):
			}
		}
	}

	grabbed := 0
	for _, o := range outcomes {
		if o.Grabbed {
			grabbed++
		}
	}
	if len(outcomes) > 0 {
		slog.Info("wanted search complete", "searched", len(outcomes), "grabbed", grabbed)
	}
	return outcomes, nil
}

// RunPeriodic searches the wanted list on the interval until ctx is
// cancelled. It quietly does nothing when no indexers are enabled.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			enabled, err := s.indexers.Store().ListEnabled()
			if err != nil || len(enabled) == 0 {
				continue
			}
			if _, err := s.SearchWanted(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("wanted search failed", "error", err)
			}
		}
	}
}
