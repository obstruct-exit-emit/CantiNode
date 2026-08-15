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
		}
	}
	return result
}

// searchAndGrab searches every enabled indexer for wanted, scores the
// results against the active music quality profile exactly like the
// manual search endpoint does, and grabs the best approved candidate if
// one exists. Returns whether it actually grabbed something.
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

	candidates := candidatesearch.ScoreAndRank(found, blocked, prefs, artist.Name)
	if len(candidates) == 0 || !candidates[0].Approved {
		return false
	}
	best := candidates[0]

	// Claim before grabbing, not after: this sweep runs unattended on a
	// timer and can land at the same moment a user manually searches and
	// grabs the same wanted album themselves. The claim is a
	// compare-and-swap (status must still be "wanted"), so only one of the
	// two ever actually proceeds to grab — and since it already sets
	// status to "downloading", a successful grab needs no separate status
	// write afterward (nothing left to fail silently and cause a
	// duplicate re-grab next sweep, unlike the blind grab-then-set-status
	// this replaced).
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

	gctx, gcancel := context.WithTimeout(ctx, grabTimeout)
	defer gcancel()
	_, _, err = s.downloads.GrabRelease(gctx, best.Protocol, best.DownloadURL, best.Title, best.GUID, wanted.ID, 0, "music")
	if err != nil {
		s.logger.Warn("autosearch: grab failed", "artist", artist.Name, "album", wanted.Title, "release", best.Title, "error", err)
		if revertErr := s.music.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusWanted); revertErr != nil {
			s.logger.Error("autosearch: revert wanted album claim after failed grab", "wanted_album_id", wanted.ID, "error", revertErr)
		}
		return false
	}
	s.logger.Info("autosearch: grabbed", "artist", artist.Name, "album", wanted.Title, "release", best.Title, "score", best.Score)
	return true
}
