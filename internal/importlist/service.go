package importlist

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/lastfm"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// PollInterval is the default sync cadence — matches Lidarr's own default
// list-sync interval. Kept here too since that's what a caller with no
// config (e.g. a test) falls back to; the real default lives in
// config.TimingSettings.ImportListSyncInterval.
const PollInterval = 24 * time.Hour

// lastFMResultLimit caps how many of a user's/tag's top artists a single
// Last.fm-backed list resolves per sync — generous enough to cover a real
// music fan's regular rotation without turning one list into hundreds of
// LookupArtist round trips against MusicBrainz's shared ~1.1s throttle.
const lastFMResultLimit = 50

// Service ties the import-list store to MusicBrainz/Last.fm resolution and
// the same artist-monitoring primitives internal/api's "Add artist" uses,
// for the periodic sync.
type Service struct {
	store       *Store
	mb          *musicbrainz.Client
	music       *musiclibrary.Store
	discography *discography.Service
	lastfm      *lastfm.Client
	httpClient  *http.Client
	logger      *slog.Logger
}

func New(store *Store, mb *musicbrainz.Client, music *musiclibrary.Store, disc *discography.Service, lastfmClient *lastfm.Client) *Service {
	return &Service{
		store:       store,
		mb:          mb,
		music:       music,
		discography: disc,
		lastfm:      lastfmClient,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		logger:      slog.Default(),
	}
}

// Store exposes the backing CRUD store for internal/api's REST handlers —
// same accessor shape as internal/indexer.Service.Store().
func (s *Service) Store() *Store { return s.store }

// RunPeriodic syncs immediately (so a fresh start doesn't wait a full
// cycle to catch up), then waits for whatever next reports and syncs
// again, until ctx is canceled — the same shape as
// internal/discoveryrefresh.Service.RunPeriodic, see its own doc comment
// for why next is a closure over live settings rather than a duration
// baked in at startup.
func (s *Service) RunPeriodic(ctx context.Context, next func(now time.Time) time.Time) {
	if next == nil {
		next = func(now time.Time) time.Time { return now.Add(PollInterval) }
	}
	s.PollOnce(ctx)
	for {
		wait := time.Until(next(time.Now()))
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.PollOnce(ctx)
		}
	}
}

// PollResult summarizes one sync pass, for logging/testing.
type PollResult struct {
	Checked int
	Added   int
}

// PollOnce resolves every enabled import list and adds+monitors any
// resolved artist not already monitored. One list's resolve failure (a
// dead network, an invalid series/user/tag) is recorded on that list's own
// row and does not stop the rest — the same non-aborting pattern
// internal/discoveryrefresh.Service.PollOnce uses. Artists already
// monitored (by any source, not just a previous list sync) are looked up
// once per pass rather than re-fetched from MusicBrainz per list, so a
// popular Last.fm list mostly full of artists the user already owns costs
// one cheap DB read, not fifty redundant lookups.
//
// Every enabled list resolves in its own goroutine rather than one after
// another — found live: a list with a few hundred lines (a plausible
// "pasted a big artist list" case) can take several minutes to resolve,
// one MusicBrainz round trip per unresolved line/artist against the
// client's own shared ~1.1s throttle, and that time used to delay every
// other enabled list in the same sweep by the same amount — even an
// otherwise-instant one positioned after it in the list. The shared
// throttle still serializes the real network requests either way, so this
// doesn't change the sweep's total work, only which list's turn is next:
// a small list is no longer stuck behind a big one. mu guards the shared
// monitored map and result, both read/written from every list's goroutine.
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult

	lists, err := s.store.List()
	if err != nil {
		s.logger.Error("importlist: list import lists", "error", err)
		return result
	}

	existing, err := s.music.ListArtists()
	if err != nil {
		s.logger.Error("importlist: list artists", "error", err)
		return result
	}
	var mu sync.Mutex
	monitored := make(map[string]bool, len(existing))
	for _, a := range existing {
		if a.IsMonitored {
			monitored[a.MBID] = true
		}
	}

	var wg sync.WaitGroup
	for _, il := range lists {
		if !il.Enabled {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		mu.Lock()
		result.Checked++
		mu.Unlock()

		wg.Add(1)
		go func(il ImportList) {
			defer wg.Done()
			s.syncOneList(ctx, il, &mu, monitored, &result)
		}(il)
	}
	wg.Wait()
	return result
}

// syncOneList is PollOnce's own per-list body, run concurrently with every
// other enabled list's — see PollOnce's own doc comment for why.
func (s *Service) syncOneList(ctx context.Context, il ImportList, mu *sync.Mutex, monitored map[string]bool, result *PollResult) {
	mbids, err := s.resolve(ctx, il)
	if err != nil {
		s.logger.Warn("importlist: resolve failed", "list", il.Name, "error", err)
		if serr := s.store.SetSyncResult(il.ID, time.Now().UTC().Format(time.RFC3339), err.Error()); serr != nil {
			s.logger.Warn("importlist: record sync failure", "list", il.Name, "error", serr)
		}
		return
	}

	for _, mbid := range mbids {
		// Claimed before releasing the lock, not after addArtist succeeds —
		// two lists can share the same not-yet-monitored artist (a popular
		// one on both a Last.fm list and a plain list, say) and now run
		// concurrently, so claiming only on success would let both call
		// addArtist for the same mbid at once, double-counting Added (the
		// end library state would still be correct either way — GetOrCreateArtist/
		// SetArtistMonitored/RefreshArtist are all idempotent — but the
		// reported count wouldn't be). The cost: if the claiming list's own
		// addArtist then fails, a sibling list sharing that mbid no longer
		// gets a same-sweep retry the way sequential PollOnce once allowed —
		// acceptable, since the next sweep (24h later) tries it again either way.
		mu.Lock()
		already := monitored[mbid]
		if !already {
			monitored[mbid] = true
		}
		mu.Unlock()
		if already {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if err := s.addArtist(ctx, mbid); err != nil {
			s.logger.Warn("importlist: add artist", "list", il.Name, "mbid", mbid, "error", err)
			continue
		}
		mu.Lock()
		result.Added++
		mu.Unlock()
	}
	if serr := s.store.SetSyncResult(il.ID, time.Now().UTC().Format(time.RFC3339), ""); serr != nil {
		s.logger.Warn("importlist: record sync result", "list", il.Name, "error", serr)
	}
}

// Resolve is PollOnce's own per-list resolution step, exported so
// handleTestImportList can validate an unsaved draft the same way a real
// sync would, without adding anything.
func (s *Service) Resolve(ctx context.Context, il ImportList) ([]string, error) {
	return s.resolve(ctx, il)
}

func (s *Service) resolve(ctx context.Context, il ImportList) ([]string, error) {
	switch il.Type {
	case TypeMusicBrainzSeries:
		return s.resolveMusicBrainzSeries(ctx, il)
	case TypeList:
		return s.resolvePlainList(ctx, il)
	case TypeLastFM:
		return s.resolveLastFM(ctx, il)
	default:
		return nil, fmt.Errorf("unknown import list type %q", il.Type)
	}
}

// resolveMusicBrainzSeries resolves il's series membership to the distinct
// real artist MBIDs behind each entry — reuses
// musicbrainz.Client.LookupSeries directly rather than tracking the series
// itself as a library entity (see ROADMAP's Import Lists write-up for why
// this replaced the removed "paste series link" synthetic-artist feature).
func (s *Service) resolveMusicBrainzSeries(ctx context.Context, il ImportList) ([]string, error) {
	series, err := s.mb.LookupSeries(ctx, il.SeriesMBID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var mbids []string
	for _, rel := range series.Relations {
		if len(rel.ArtistCredit) == 0 {
			continue
		}
		id := rel.ArtistCredit[0].Artist.ID
		if id == "" || id == musicbrainz.VariousArtistsMBID || seen[id] {
			continue
		}
		seen[id] = true
		mbids = append(mbids, id)
	}
	return mbids, nil
}

// resolvePlainList resolves il's pasted (or fetched) one-name-per-line
// text to MusicBrainz artist MBIDs via the same fuzzy search "add artist"
// itself uses. A name with no match is logged and skipped, not treated as
// a whole-list failure — the same best-effort pattern PollOnce uses across
// lists, applied within one list's own entries.
func (s *Service) resolvePlainList(ctx context.Context, il ImportList) ([]string, error) {
	text := il.ListText
	if strings.TrimSpace(il.SourceURL) != "" {
		fetched, err := s.fetchListText(ctx, il.SourceURL)
		if err != nil {
			return nil, err
		}
		text = fetched
	}

	seen := map[string]bool{}
	var mbids []string
	for _, name := range parseListLines(text) {
		if ctx.Err() != nil {
			return mbids, ctx.Err()
		}
		results, err := s.mb.SearchArtists(ctx, name)
		if err != nil || len(results) == 0 {
			s.logger.Warn("importlist: no MusicBrainz match", "name", name, "error", err)
			continue
		}
		id := results[0].ID
		if seen[id] {
			continue
		}
		seen[id] = true
		mbids = append(mbids, id)
	}
	return mbids, nil
}

// parseListLines splits raw list text into candidate artist names — one
// per line, blank lines and "#"-prefixed comment lines skipped.
func parseListLines(text string) []string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	return names
}

func (s *Service) fetchListText(ctx context.Context, sourceURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", sourceURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d", sourceURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	return string(body), nil
}

// resolveLastFM resolves il's Last.fm top-artists list to MusicBrainz
// artist MBIDs — Last.fm's own mbid field when it has one (most popular
// artists do), a MusicBrainz name search otherwise.
func (s *Service) resolveLastFM(ctx context.Context, il ImportList) ([]string, error) {
	var artists []lastfm.TopArtist
	var err error
	if il.LastfmKind == LastfmKindTag {
		artists, err = s.lastfm.TopArtistsForTag(ctx, il.LastfmTarget, lastFMResultLimit)
	} else {
		artists, err = s.lastfm.TopArtistsForUser(ctx, il.LastfmTarget, lastFMResultLimit)
	}
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var mbids []string
	for _, a := range artists {
		if ctx.Err() != nil {
			return mbids, ctx.Err()
		}
		id := a.MBID
		if id == "" {
			results, err := s.mb.SearchArtists(ctx, a.Name)
			if err != nil || len(results) == 0 {
				s.logger.Warn("importlist: no MusicBrainz match for Last.fm artist", "name", a.Name, "error", err)
				continue
			}
			id = results[0].ID
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		mbids = append(mbids, id)
	}
	return mbids, nil
}

// addArtist is the same cheap "add and monitor" sequence
// internal/api.handleQuickAddMusicArtist uses: look the artist up,
// upsert its row, monitor it, and cache just its discography — bio/photo
// and per-release-group version/tracklist caching are left to the
// existing internal/metadatabackfill periodic sweep, the same catch-up
// path a quick-added artist already relies on, rather than paying for
// them synchronously per artist during an unattended sweep of potentially
// many artists at once.
func (s *Service) addArtist(ctx context.Context, mbid string) error {
	mbArtist, err := s.mb.LookupArtist(ctx, mbid)
	if err != nil {
		return err
	}
	a, err := s.music.GetOrCreateArtist(mbid, mbArtist.Name, mbArtist.SortName)
	if err != nil {
		return err
	}
	if err := s.music.SetArtistMonitored(a.ID, true); err != nil {
		return err
	}
	_, err = s.discography.RefreshArtist(ctx, a.ID, mbArtist)
	return err
}
