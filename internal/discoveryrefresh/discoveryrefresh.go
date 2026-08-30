// Package discoveryrefresh periodically re-caches every monitored artist's
// own discography from MusicBrainz, so a new release shows up in that
// artist's Missing section without the user
// having to click "Refresh metadata" by hand — the automatic half of
// release monitoring internal/autosearch doesn't cover: autosearch only
// ever sweeps albums already sitting in wanted_albums, it never discovers
// a release group that isn't already cached.
//
// Deliberately narrow, mirroring internal/autosearch's own scope
// decision: only the cheap discography-only refresh
// (internal/discography.Service.Refresh — one MusicBrainz request per
// artist in the common case), never the heavier per-release-group
// version/tracklist caching or TheAudioDB bio/photo fetch, which stay on
// their existing manual-refresh/backfill-sweep paths. Running those
// unconditionally across every monitored artist on a timer could
// seriously strain MusicBrainz's shared ~1.1s request throttle for a
// large library. A newly-discovered release lands in Missing only — no
// auto-want, matching the deliberate choice already made for monitoring
// in general (see internal/autosearch's own package doc comment).
package discoveryrefresh

import (
	"context"
	"log/slog"
	"time"

	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// PollInterval is the default sweep cadence — matches Lidarr's own
// default "Refresh Artist" task interval. Kept here too since that's what
// a caller with no config (e.g. a test) falls back to; the real default
// lives in config.TimingSettings.DiscographyRefreshInterval.
const PollInterval = 24 * time.Hour

// Service ties the music store and discography caching together for the
// periodic sweep.
type Service struct {
	music       *musiclibrary.Store
	discography *discography.Service
	logger      *slog.Logger
}

func New(music *musiclibrary.Store, disc *discography.Service) *Service {
	return &Service{music: music, discography: disc, logger: slog.Default()}
}

// RunPeriodic sweeps immediately (so a fresh start doesn't wait a full
// cycle to catch up), then waits for whatever next reports and sweeps
// again, until ctx is canceled — the exact same shape as
// internal/autosearch.Service.RunPeriodic, see its own doc comment for
// why next is a closure over live settings rather than a duration baked
// in at startup.
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
	Checked   int
	Refreshed int
}

// PollOnce re-caches every monitored artist's own discography, one at a
// time. A single artist's refresh failure is recorded in the log and does
// not stop the sweep — the same non-aborting pattern
// internal/autosearch.Service.PollOnce uses.
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult

	artists, err := s.music.ListArtists()
	if err != nil {
		s.logger.Error("discoveryrefresh: list artists", "error", err)
		return result
	}

	for _, artist := range artists {
		if !artist.IsMonitored {
			continue
		}
		if ctx.Err() != nil {
			return result
		}
		result.Checked++
		if err := s.discography.Refresh(ctx, &artist); err != nil {
			s.logger.Warn("discoveryrefresh: refresh failed", "artist", artist.Name, "error", err)
			continue
		}
		result.Refreshed++
	}
	return result
}
