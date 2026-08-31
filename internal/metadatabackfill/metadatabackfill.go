// Package metadatabackfill fills in discography/bio/photo metadata for
// artists that only ever got a bare name/MBID row — an artist a scan
// discovered implicitly by matching a file (internal/musicscanner's
// matchFolder/matchFileFuzzy create the artist row directly via the store,
// with no MusicBrainz or TheAudioDB metadata beyond that). Explicitly
// monitoring an artist already caches everything up front (see
// internal/api's handleMonitorMusicArtist, which calls
// CacheFullArtistMetadata directly); this package is the "added implicitly"
// counterpart, and runs on its own periodic timer independent of any
// particular scan.
//
// That independence is the whole point: before this package existed, this
// same sweep only ever ran once, synchronously, right after a library scan
// finished — tied to that scan's own goroutine. A process restart mid-sweep
// (a redeploy, a crash, a routine reboot) killed it outright, and nothing
// ever resumed it until the next full scan, which a user may not run again
// for a long time. The sweep itself was always idempotent (it already skips
// any artist with MetadataFetchedAt already set — see PollOnce), so the gap
// was purely "nothing re-triggers it after an interruption," not the sweep
// logic. Giving it its own periodic loop closes that gap: an interrupted
// pass is always picked up again within one PollInterval, no matter what
// killed it.
//
// Also owns the heavier per-release-group version/tracklist caching (see
// CacheReleaseGroupVersions/CacheAllVersionTracklists/CacheDiscographyVersions)
// — deliberately kept out of internal/discography, whose own doc comment
// disclaims it precisely so its periodic sweep stays cheap; this package is
// the "callers' own responsibility" that comment refers to.
package metadatabackfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cantinode/cantinode/internal/audiodb"
	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// PollInterval is how often the backstop sweep checks for artists still
// missing metadata — deliberately short relative to discoveryrefresh's own
// interval, since a no-op pass (the common case: nothing pending) is just
// one cheap ListArtists call. Not exposed as a setting, same reasoning as
// internal/importer.PollInterval: a fixed, reasonable default rather than
// one more knob.
const PollInterval = 15 * time.Minute

// Service ties the music store, MusicBrainz client, TheAudioDB client, and
// discography caching together for both the on-demand "add/refresh an
// artist" path (CacheFullArtistMetadata/RefreshArtist) and the periodic
// backfill sweep (PollOnce/RunPeriodic).
type Service struct {
	music       *musiclibrary.Store
	mb          *musicbrainz.Client
	audiodb     *audiodb.Client
	discography *discography.Service
	logger      *slog.Logger

	// pollMu serializes PollOnce against itself. Two independent call
	// sites can each trigger a full backfill sweep on this same Service —
	// the periodic RunPeriodic ticker and internal/api's own post-scan
	// call (handleTriggerMusicScan, right after ScanAll) — with no
	// coordination between them. Found live: a scan long enough to cross
	// the 15-minute periodic boundary (a first/large scan, or a big
	// import-list sync landing right before a tick) let both sweeps list
	// the same not-yet-fetched artists and both start refreshing them at
	// once, doubling MusicBrainz/TheAudioDB request load — including two
	// independent detached CacheDiscographyVersions goroutines per
	// artist — at exactly the moment the backlog is largest. A second
	// caller waiting its turn (rather than running concurrently) means
	// it sees those artists' MetadataFetchedAt already stamped by the
	// first pass and simply skips them, the same self-healing effect
	// scanMu already gets Scanner.ScanAll/ScanAlbumFolder.
	pollMu sync.Mutex
}

func New(music *musiclibrary.Store, mb *musicbrainz.Client, audiodbClient *audiodb.Client, disc *discography.Service) *Service {
	return &Service{music: music, mb: mb, audiodb: audiodbClient, discography: disc, logger: slog.Default()}
}

// RunPeriodic sweeps immediately (so a fresh start doesn't wait a full
// interval to catch up on anything an interrupted previous pass left
// behind), then again every interval until ctx is canceled. interval <= 0
// uses PollInterval.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = PollInterval
	}
	s.PollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollOnce(ctx)
		}
	}
}

// PollResult summarizes one PollOnce pass, for logging/testing.
type PollResult struct {
	Checked int
	Cached  int
}

// PollOnce fills in metadata for every artist that doesn't have it yet —
// MetadataFetchedAt is set the first time regardless of outcome (even an
// artist TheAudioDB has nothing for), so it's never retried on every
// subsequent pass. Best-effort: one artist's failure (a dead network,
// MusicBrainz/TheAudioDB down) is logged and skipped rather than aborting
// the rest.
//
// Serialized via pollMu against a concurrent PollOnce call from the other
// trigger (RunPeriodic's own ticker vs. internal/api's post-scan call) —
// see pollMu's own doc comment.
func (s *Service) PollOnce(ctx context.Context) PollResult {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	var result PollResult

	artists, err := s.music.ListArtists()
	if err != nil {
		s.logger.Warn("metadatabackfill: listing artists", "error", err)
		return result
	}

	for _, a := range artists {
		if a.MetadataFetchedAt != nil {
			continue
		}
		if ctx.Err() != nil {
			return result
		}
		result.Checked++
		if err := s.RefreshArtist(ctx, a.ID, a.MBID); err != nil {
			s.logger.Warn("metadatabackfill: caching metadata for artist", "artist", a.Name, "error", err)
			continue
		}
		result.Cached++
	}
	return result
}

// RefreshArtist caches an already-known artistID's entire metadata set —
// discography, genres/tags/rating, versions/tracklists, bio/image — given
// only its mbid. Looks the artist up once, then hands off to
// CacheFullArtistMetadata for the rest; a caller that already looked the
// artist up itself (internal/api's handleMonitorMusicArtist) should call
// CacheFullArtistMetadata directly instead, to skip repeating that lookup.
func (s *Service) RefreshArtist(ctx context.Context, artistID int64, mbid string) error {
	mbArtist, err := s.mb.LookupArtist(ctx, mbid)
	if err != nil {
		return err
	}
	return s.CacheFullArtistMetadata(ctx, artistID, mbArtist)
}

// CacheFullArtistMetadata is the complete "add or refresh an artist" job
// given an already-looked-up mbArtist: the discography baseline, plus
// backgrounded per-release-group version/tracklist caching and a
// best-effort TheAudioDB bio/image fetch. A TheAudioDB failure is never
// fatal — the MusicBrainz side alone is enough to succeed.
func (s *Service) CacheFullArtistMetadata(ctx context.Context, artistID int64, mbArtist *musicbrainz.Artist) error {
	groups, err := s.discography.RefreshArtist(ctx, artistID, mbArtist)
	if err != nil {
		return err
	}

	// Eagerly cache every release group's known versions AND every one of
	// those versions' own full tracklist, in the background — the point is
	// that browsing Missing/Wanted, or picking a specific release version
	// in the matching UI, afterward never calls MusicBrainz at all; only
	// this (monitor, an explicit "Refresh metadata", or the periodic
	// sweep) does. Backgrounded because a release group can have many
	// versions and each tracklist costs a further MusicBrainz request at
	// its ~1/sec rate limit, so a prolific artist's full discography can
	// take minutes to hours — far too long to hold this call (or the scan
	// that may have triggered it) open for. Detached from ctx (which dies
	// the moment this call returns).
	go s.CacheDiscographyVersions(context.Background(), groups)

	meta, err := s.audiodb.LookupArtistByMBID(ctx, mbArtist.ID)
	if err != nil {
		// Transient failure (network, TheAudioDB down) — cosmetic, not fatal,
		// and leaves MetadataFetchedAt unset so a later sweep or explicit
		// refresh tries again rather than treating this as a permanent miss.
		return nil
	}
	bio, imageURL := "", ""
	if meta != nil {
		bio, imageURL = meta.Bio, meta.ImageURL
	}
	// Stamped even when TheAudioDB simply has nothing for this artist (a
	// definitive answer, not a failure) so PollOnce doesn't re-query it on
	// every subsequent sweep.
	return s.music.SetArtistMetadata(artistID, bio, imageURL, time.Now().UTC())
}

// CacheReleaseGroupVersions browses every known release (version/edition)
// of releaseGroupMBID and replaces its cached version list — the metadata
// a version picker needs (title/date/country/status/track count/media
// summary), without yet fetching any of their full tracklists (see
// CacheAllVersionTracklists for that). Shared by internal/api's
// resolveRepresentativeRelease (an on-demand cache-miss fallback) and the
// eager discography sweep here.
//
// Skips the MusicBrainz round trip entirely when this release group
// already has a real (non-placeholder) cached version list — found live:
// CacheDiscographyVersions's own doc comment already claimed this
// function was idempotent (every existing caller in internal/api pre-checks
// its own cache first, so it reads that way from the outside), but nothing
// here actually enforced it. CacheFullArtistMetadata's own retry path hit
// the gap directly: a transient TheAudioDB failure leaves MetadataFetchedAt
// unset specifically so a later sweep retries (see that function's own
// doc comment) — but retrying re-ran this for the artist's ENTIRE
// discography every single pass, re-fetching hundreds of release groups'
// version lists from MusicBrainz for no reason, at its ~1/sec throttle,
// forever, until TheAudioDB happened to succeed. A release group's own
// version list is effectively permanent once fetched (new editions of an
// old release are rare, and a genuinely new release group is by
// definition not yet cached, so it's never skipped here) — the same
// "never changes once cached" treatment CacheAllVersionTracklists already
// gives a version's own tracklist one layer down.
func (s *Service) CacheReleaseGroupVersions(ctx context.Context, releaseGroupMBID string) ([]musiclibrary.ReleaseGroupVersion, error) {
	if has, err := s.music.HasReleaseGroupVersions(releaseGroupMBID); err != nil {
		return nil, fmt.Errorf("check cached versions: %w", err)
	} else if has {
		return s.music.ListReleaseGroupVersions(releaseGroupMBID)
	}

	releases, err := s.mb.BrowseReleaseGroupReleases(ctx, releaseGroupMBID)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found for this release group")
	}
	best := pickRepresentativeRelease(releases)
	versions := make([]musiclibrary.ReleaseGroupVersion, 0, len(releases))
	for _, r := range releases {
		versions = append(versions, musiclibrary.ReleaseGroupVersion{
			ReleaseGroupMBID: releaseGroupMBID,
			ReleaseMBID:      r.ID,
			Title:            r.Title,
			ReleaseDate:      r.Date,
			Country:          r.Country,
			Status:           r.Status,
			Disambiguation:   r.Disambiguation,
			TrackCount:       r.TotalTrackCount(),
			MediaSummary:     r.MediaSummary(),
			IsRepresentative: best != nil && r.ID == best.ID,
		})
	}
	if err := s.music.ReplaceReleaseGroupVersions(releaseGroupMBID, versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// pickRepresentativeRelease chooses which of a release group's releases to
// show a tracklist/cover preview for, before anything's actually been
// matched to a real file: prefers a US "Official" CD edition — the
// closest thing to "what you'll most likely end up owning" for the
// typical case — over anything else. When no release is a US CD, falls
// back to preferring any "Official" release over another status
// (promos/bootlegs/pseudo-releases are frequently missing tracks or
// reordered), then the earliest dated one as a stable, deterministic
// tie-break — the "closest equivalent." Returns nil for an empty slice.
// Once a file is actually matched, its own real release always takes
// over from whatever this picked (see GetReleaseGroupVersionByRelease) —
// this only ever governs the preview shown before that happens.
func pickRepresentativeRelease(releases []musicbrainz.ReleaseSearchResult) *musicbrainz.ReleaseSearchResult {
	if usCD := pickUSCD(releases); usCD != nil {
		return usCD
	}
	var best *musicbrainz.ReleaseSearchResult
	for i := range releases {
		r := &releases[i]
		if best == nil {
			best = r
			continue
		}
		if bestOfficial, rOfficial := best.Status == "Official", r.Status == "Official"; rOfficial != bestOfficial {
			if rOfficial {
				best = r
			}
			continue
		}
		if r.Date != "" && (best.Date == "" || r.Date < best.Date) {
			best = r
		}
	}
	return best
}

// pickUSCD returns the earliest-dated Official release whose country is
// "US" and which has at least one CD medium — nil if no release matches
// all three, so pickRepresentativeRelease falls back to its own more
// general preference instead.
func pickUSCD(releases []musicbrainz.ReleaseSearchResult) *musicbrainz.ReleaseSearchResult {
	var best *musicbrainz.ReleaseSearchResult
	for i := range releases {
		r := &releases[i]
		if r.Country != "US" || r.Status != "Official" || !hasCDMedium(r) {
			continue
		}
		if best == nil || (r.Date != "" && (best.Date == "" || r.Date < best.Date)) {
			best = r
		}
	}
	return best
}

func hasCDMedium(r *musicbrainz.ReleaseSearchResult) bool {
	for _, m := range r.Media {
		if m.Format == "CD" {
			return true
		}
	}
	return false
}

// CacheAllVersionTracklists eagerly fetches and caches every one of
// versions' own full tracklist not already cached — every known edition of
// an album, not just the representative one, so the matching UI's version
// picker never needs to call MusicBrainz again once this finishes. Skips
// anything already cached (a tracklist never changes once released);
// best-effort per version, so one failure is logged and skipped rather
// than aborting the rest.
func (s *Service) CacheAllVersionTracklists(ctx context.Context, versions []musiclibrary.ReleaseGroupVersion) {
	for _, v := range versions {
		if ctx.Err() != nil {
			return
		}
		if cached, err := s.music.GetCachedTracklist(v.ReleaseMBID); err == nil {
			var probe musicbrainz.ReleaseWithTracklist
			if jerr := json.Unmarshal([]byte(cached.TracksJSON), &probe); jerr == nil {
				continue
			}
			// Row exists but isn't in the current format (e.g. migrated
			// from the old flattened tracklist cache) — fall through and
			// refetch/overwrite it rather than treating its mere presence
			// as "already warm" forever.
			s.logger.Warn("metadatabackfill: cached tracklist is unreadable, refreshing", "release", v.Title)
		} else if !errors.Is(err, musiclibrary.ErrNotFound) {
			s.logger.Warn("metadatabackfill: checking version tracklist cache", "release", v.Title, "error", err)
			continue
		}
		full, err := s.mb.LookupReleaseWithTracklist(ctx, v.ReleaseMBID)
		if err != nil {
			s.logger.Warn("metadatabackfill: fetching version tracklist", "release", v.Title, "error", err)
			continue
		}
		if err := s.StoreTracklist(v.ReleaseGroupMBID, full); err != nil {
			s.logger.Warn("metadatabackfill: caching version tracklist", "release", v.Title, "error", err)
		}
	}
}

// CacheDiscographyVersions runs CacheReleaseGroupVersions then
// CacheAllVersionTracklists for every one of groups — the full eager sweep
// run in the background after an artist's discography is (re)synced (see
// CacheFullArtistMetadata) or during internal/api's backfill sweep for an
// artist that predates this feature. Best-effort per release group, so one
// failure is logged and skipped rather than aborting the rest of the sweep.
func (s *Service) CacheDiscographyVersions(ctx context.Context, groups []musiclibrary.ReleaseGroupCache) {
	for _, g := range groups {
		if ctx.Err() != nil {
			return
		}
		versions, err := s.CacheReleaseGroupVersions(ctx, g.ReleaseGroupMBID)
		if err != nil {
			s.logger.Warn("metadatabackfill: caching release group versions", "releaseGroup", g.Title, "error", err)
			continue
		}
		s.CacheAllVersionTracklists(ctx, versions)
	}
}

// StoreTracklist marshals full and writes it through to the music store's
// per-release tracklist cache — the whole musicbrainz.ReleaseWithTracklist
// (not a hand-picked projection), so a cache hit can be decoded straight
// back into the same type musicscanner.SuggestMatches and internal/api's
// tracklist preview flattener both consume.
func (s *Service) StoreTracklist(releaseGroupMBID string, full *musicbrainz.ReleaseWithTracklist) error {
	b, err := json.Marshal(full)
	if err != nil {
		return fmt.Errorf("marshal tracklist: %w", err)
	}
	return s.music.SetCachedTracklist(full.ID, releaseGroupMBID, string(b))
}
