// Package autosearch periodically searches indexers for every monitored
// artist's still-wanted albums and grabs the best approved release —
// the automatic half of acquisition that internal/api's own wanted-album
// endpoints leave to a manual "Search releases" click. internal/importer
// is the other half: once a grab this package makes finishes, importer
// picks it up, copies the files into the library, and scans them in.
//
// Deliberately scoped to monitored artists only, mirroring the decision
// already made for wanting an album in the first place: wanting doesn't
// require monitoring (see internal/api's handleWantMusicAlbum), and the
// reverse holds here too — an unmonitored artist's wanted albums sit
// there for a human to search manually, never swept automatically.
package autosearch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cantinode/cantinode/internal/candidatesearch"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/release"
)

// PollInterval is how often the wanted list is swept — far less
// time-sensitive than internal/importer's download-progress polling (an
// album that's been wanted for an extra day isn't user-visible the way a
// stalled download is), so a long, indexer-friendly interval is the right
// default rather than something to tune down. Matches
// config.TimingSettings.WantedSearchInterval's own default; kept here too
// since that's what a caller with no config (e.g. a test) falls back to.
const PollInterval = 24 * time.Hour

// searchTimeout bounds one album's own indexer search and grab — a hung
// indexer or download client must not stall the rest of the sweep.
const searchTimeout = 90 * time.Second
const grabTimeout = 60 * time.Second

// Service ties the music domain, indexers, and download clients together
// for the periodic sweep.
type Service struct {
	music     *musiclibrary.Store
	indexers  *indexer.Service
	downloads *download.Service
	store     *library.Store
	logger    *slog.Logger

	// InterAlbumDelay paces consecutive per-album searches within one
	// sweep — zero (the default every test gets, unchanged) means no
	// pacing at all; cmd/cantinode/main.go sets this to a real delay for
	// the production service. Found live: each wanted album's own
	// searchAndGrab fans out to every enabled indexer with zero delay to
	// the next album, so a large backlog (e.g. right after a big
	// import-list add, since RunPeriodic sweeps immediately on every
	// startup) drove continuous rapid-fire queries at every configured
	// indexer/Prowlarr for the whole sweep's duration — each indexer's own
	// backoff (internal/indexer) only engages after 3 *consecutive*
	// failures, so nothing here protected against a real indexer that
	// rate-limits per-minute rather than per-request. Only ever set once
	// at startup, before RunPeriodic's own goroutine starts reading it, so
	// no synchronization is needed for the field itself.
	InterAlbumDelay time.Duration
}

func New(music *musiclibrary.Store, indexers *indexer.Service, downloads *download.Service, store *library.Store) *Service {
	return &Service{music: music, indexers: indexers, downloads: downloads, store: store, logger: slog.Default()}
}

// RunPeriodic sweeps immediately (so a fresh start doesn't wait a full
// cycle to catch up), then waits for whatever next reports and sweeps
// again, until ctx is canceled. next is called fresh before each wait —
// given "now", it returns the next time to fire — so it can express either
// a fixed interval (now.Add(d)) or a daily fire time (see
// config.TimingSettings.WantedSearchNextRun, main's actual caller): a
// closure over live settings rather than a single duration baked in at
// startup keeps every wait computed from the real clock, self-correcting
// instead of drifting. nil uses a plain PollInterval ticker.
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

// PollResult summarizes one sweep pass, for logging/testing.
type PollResult struct {
	Checked int
	Grabbed int
}

// PollOnce searches every monitored artist's still-wanted (not already
// downloading) albums, one at a time, grabbing the best approved release
// found. A single album's search/grab failure is recorded in the log and
// does not stop the sweep — the same non-aborting pattern
// internal/importer's own PollOnce uses; nothing approved this pass just
// means it's tried again next sweep.
//
// The blocklist and the active quality profile are each fetched once for
// the whole sweep, not once per album — both are constant for the sweep's
// duration, so re-querying them per album (as an earlier version of this
// function did) was pure repeated DB I/O for no benefit.
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult

	artists, err := s.music.ListArtists()
	if err != nil {
		s.logger.Error("autosearch: list artists", "error", err)
		return result
	}
	blocked, err := s.downloads.Store().BlockedKeys()
	if err != nil {
		s.logger.Error("autosearch: list blocklist", "error", err)
		return result
	}
	prefs := release.PreferencesFor(s.store, "music")

	for _, artist := range artists {
		if !artist.IsMonitored {
			continue
		}
		if ctx.Err() != nil {
			return result
		}
		wanted, err := s.music.ListWantedAlbumsByArtist(artist.ID)
		if err != nil {
			s.logger.Error("autosearch: list wanted albums", "artist", artist.Name, "error", err)
			continue
		}
		for _, w := range wanted {
			if w.Status != musiclibrary.WantedStatusWanted {
				continue // already downloading — nothing to search for
			}
			if ctx.Err() != nil {
				return result
			}
			result.Checked++
			if s.searchAndGrab(ctx, artist, w, blocked, prefs) {
				result.Grabbed++
			}
			if s.InterAlbumDelay > 0 {
				select {
				case <-ctx.Done():
					return result
				case <-time.After(s.InterAlbumDelay):
				}
			}
		}
	}
	return result
}

// maxGrabAttemptsPerAlbum bounds searchAndGrab's own retry loop below — a
// real safety net, not a number expected to matter in practice: the
// blocklist already shrinks what ScoreAndRank approves on any later call,
// so a long run of consecutive untrackable candidates in one single pass
// would mean something structurally wrong (a misbehaving indexer flooding
// results, say), not routine bad luck worth chasing further.
const maxGrabAttemptsPerAlbum = 5

// searchAndGrab searches every enabled indexer for wanted, scores the
// results against the active music quality profile exactly like the
// manual search endpoint does, and grabs the best approved candidate —
// trying the next-best one immediately, in this same pass, whenever a
// grab fails in a way that's specifically this release's own fault (see
// download.ErrNoTrackableID) rather than leaving the identical release to
// be picked — and fail the identical way — again next sweep. Returns
// whether it actually grabbed something.
func (s *Service) searchAndGrab(ctx context.Context, artist musiclibrary.Artist, wanted musiclibrary.WantedAlbum, blocked map[string]bool, prefs release.Preferences) bool {
	sctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	query := artist.Name + " " + wanted.Title
	found, errs, err := s.indexers.SearchAll(sctx, query, wanted.Title, "music")
	if err != nil {
		s.logger.Warn("autosearch: search failed", "artist", artist.Name, "album", wanted.Title, "error", err)
		return false
	}
	if len(errs) > 0 {
		s.logger.Warn("autosearch: some indexers failed to answer", "artist", artist.Name, "album", wanted.Title, "errors", errs)
	}

	candidates := candidatesearch.ScoreAndRank(found, blocked, prefs, artist.SearchRelevanceName())
	if len(candidates) == 0 || !candidates[0].Approved {
		return false
	}

	// Claim before grabbing, not after: this sweep runs unattended on a
	// timer and can land at the same moment a user manually searches and
	// grabs the same wanted album themselves. The claim is a
	// compare-and-swap (status must still be "wanted"), so only one of the
	// two ever actually proceeds to grab — made once, up front, and held
	// across every candidate attempt below (still the same one sweep
	// pass, nothing has changed hands between attempts).
	claimed, err := s.music.ClaimWantedAlbumForDownload(wanted.ID)
	if err != nil {
		s.logger.Error("autosearch: claim wanted album", "wanted_album_id", wanted.ID, "error", err)
		return false
	}
	if !claimed {
		s.logger.Info("autosearch: album was grabbed elsewhere just before this sweep reached it, skipping",
			"artist", artist.Name, "album", wanted.Title)
		return false
	}

	attempts := 0
	for _, best := range candidates {
		if !best.Approved || attempts >= maxGrabAttemptsPerAlbum {
			break // candidates is ranked approved-first (release.Rank); nothing after the first unapproved one is worth trying either
		}
		attempts++

		gctx, gcancel := context.WithTimeout(ctx, grabTimeout)
		_, _, err = s.downloads.GrabRelease(gctx, best.Protocol, best.DownloadURL, best.Title, best.GUID, wanted.ID, 0, "music")
		gcancel()
		if err == nil {
			s.logger.Info("autosearch: grabbed", "artist", artist.Name, "album", wanted.Title, "release", best.Title, "score", best.Score)
			return true
		}

		if errors.Is(err, download.ErrNoTrackableID) {
			// This release specifically is the problem, not the attempt —
			// blocklist it and try the next-best candidate immediately,
			// rather than leaving the exact same release to be picked
			// again next sweep.
			s.logger.Warn("autosearch: grab landed with no trackable id, blocklisting and trying the next candidate",
				"artist", artist.Name, "album", wanted.Title, "release", best.Title, "error", err)
			if blockErr := s.downloads.Store().AddBlock(best.GUID, best.Title, "grab reported no trackable id"); blockErr != nil {
				s.logger.Error("autosearch: blocklist untrackable release", "release", best.Title, "error", blockErr)
			}
			continue
		}

		// Any other failure (client unreachable, bad credentials, ...) is
		// environmental, not this release's fault — stop here rather than
		// burning through every other candidate for the same underlying
		// reason.
		s.logger.Warn("autosearch: grab failed", "artist", artist.Name, "album", wanted.Title, "release", best.Title, "error", err)
		break
	}

	if revertErr := s.music.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusWanted); revertErr != nil {
		s.logger.Error("autosearch: revert wanted album claim after failed grab", "wanted_album_id", wanted.ID, "error", revertErr)
	}
	return false
}
