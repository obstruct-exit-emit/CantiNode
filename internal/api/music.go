// Music: artists/albums/tracks browsing, monitoring, scanning, manual
// matching, and organizing — ported from CantiNode's own original,
// from-scratch API (before this codebase was rebuilt on top of a fork of
// LibriNode), adapted to musiclibrary/musicscanner and to CantiNode's
// existing indexer/download-client pipeline instead of the old standalone
// Prowlarr/qBittorrent/SABnzbd clients.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/candidatesearch"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/coverart"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
	"github.com/cantinode/cantinode/internal/release"
)

// musicNotFoundStatus maps musiclibrary.ErrNotFound to 404, anything else
// to 500 — the musiclibrary-store counterpart to writeStoreError.
func musicNotFoundStatus(err error) int {
	if errors.Is(err, musiclibrary.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func writeMusicStoreError(w http.ResponseWriter, err error) {
	writeError(w, musicNotFoundStatus(err), err.Error())
}

// --- Artists ---

// handleListMusicArtists serves the library grid — each artist annotated
// with its owned/total album counts (see musicArtistDetail) in bulk, one
// query each rather than one round trip per artist.
func (s *server) handleListMusicArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := s.musicStore.ListArtists()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	owned, err := s.musicStore.CountOwnedAlbumsByArtist()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wanted, err := s.musicStore.CountWantedAlbumsByArtist()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]musicArtistDetail, len(artists))
	for i, a := range artists {
		out[i] = musicArtistDetail{Artist: a, OwnedAlbumCount: owned[a.ID], TotalAlbumCount: owned[a.ID] + wanted[a.ID]}
	}
	writeJSON(w, http.StatusOK, out)
}

// musicArtistDetail is musiclibrary.Artist plus its owned/total album
// counts — the artist page's header, and (owned/total only) the library
// grid's poster-card subtitle. TotalAlbumCount is owned + wanted,
// deliberately NOT the artist's entire cached discography — an artist
// with a big catalog and nothing else wanted should read as "1/1", not
// "1/126" against every release group Missing would otherwise list. 0 for
// an artist that owns and wants nothing at all, in which case the grid
// falls back to showing just the owned count.
type musicArtistDetail struct {
	musiclibrary.Artist
	OwnedAlbumCount int `json:"ownedAlbumCount"`
	TotalAlbumCount int `json:"totalAlbumCount"`
}

func (s *server) handleGetMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := s.musicStore.GetArtist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	albums, err := s.musicStore.ListAlbumsByArtist(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wanted, err := s.musicStore.ListWantedAlbumsByArtist(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, musicArtistDetail{Artist: *a, OwnedAlbumCount: len(albums), TotalAlbumCount: len(albums) + len(wanted)})
}

// handleSearchMusicArtists proxies a fuzzy artist search to MusicBrainz —
// lets the "monitor an artist" UI resolve a plain-text name to an MBID
// before calling handleMonitorMusicArtist.
func (s *server) handleSearchMusicArtists(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("query")
	if name == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	artists, err := s.mb.SearchArtists(ctx, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, artists)
}

// handleMonitorMusicArtist starts watching an artist CantiNode may not know
// about at all yet — looks it up on MusicBrainz, upserts its row (works
// whether or not it already owns files), flips IsMonitored on, and caches
// its discography plus a best-effort bio/image fetch.
func (s *server) handleMonitorMusicArtist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MBID string `json:"mbid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MBID == "" {
		writeError(w, http.StatusBadRequest, "mbid is required")
		return
	}
	ctx, cancel := s.artistRefreshCtx(r)
	defer cancel()

	mbArtist, err := s.mb.LookupArtist(ctx, req.MBID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "look up artist: "+err.Error())
		return
	}
	a, err := s.musicStore.GetOrCreateArtist(req.MBID, mbArtist.Name, mbArtist.SortName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.musicStore.SetArtistMonitored(a.ID, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Passes the mbArtist already looked up above straight through, rather
	// than going via metadataBackfill.RefreshArtist (which only has an mbid
	// to work with, from callers — handleRefreshMusicArtist — that never
	// looked one up themselves) and repeating an identical MusicBrainz
	// request for data already in hand.
	if err := s.metadataBackfill.CacheFullArtistMetadata(ctx, a.ID, mbArtist); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a, err = s.musicStore.GetArtist(a.ID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// handleQuickAddMusicArtist is handleMonitorMusicArtist's lighter sibling
// for the Unmatched Files auto-match panel's "artist not in your library"
// search: creates and monitors the artist with just the discography
// cached (what the panel's own Album dropdown needs right away to keep
// matching moving), skipping the backgrounded per-release-group
// version/tracklist pre-fetch and the TheAudioDB bio/photo lookup that
// handleMonitorMusicArtist also does — neither is needed to finish
// matching a file, and the bio/photo lookup in particular is a real,
// synchronous extra network round trip mid-workflow. MetadataFetchedAt is
// deliberately left unset, so the artist looks exactly like one added
// before this feature existed: internal/metadatabackfill's own periodic
// sweep (or the very next scan, whichever comes first) finds it and
// finishes the job (bio/photo, and — via CacheDiscographyVersions — every
// release group's versions and tracklists) automatically, no separate
// "catch up later" mechanism needed here.
func (s *server) handleQuickAddMusicArtist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MBID string `json:"mbid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MBID == "" {
		writeError(w, http.StatusBadRequest, "mbid is required")
		return
	}
	ctx, cancel := s.artistRefreshCtx(r)
	defer cancel()

	mbArtist, err := s.mb.LookupArtist(ctx, req.MBID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "look up artist: "+err.Error())
		return
	}
	a, err := s.musicStore.GetOrCreateArtist(req.MBID, mbArtist.Name, mbArtist.SortName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.musicStore.SetArtistMonitored(a.ID, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.cacheArtistDiscography(ctx, a.ID, mbArtist); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a, err = s.musicStore.GetArtist(a.ID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// seriesURLPattern extracts a series MBID from a pasted MusicBrainz series
// URL (any host/scheme/www-prefix — a self-hosted MusicBrainz mirror is
// exactly why this doesn't hardcode musicbrainz.org, matching
// internal/musicbrainz.NewClientWithBaseURL's own reasoning). bareMBIDPattern
// covers pasting the raw MBID directly.
var (
	seriesURLPattern = regexp.MustCompile(`(?i)/series/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	bareMBIDPattern  = regexp.MustCompile(`(?i)^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
)

// extractSeriesMBID parses a series MBID out of raw pasted input — a full
// MusicBrainz series URL or a bare MBID. Done server-side only: the
// backend is the only place that can authoritatively accept or reject this
// anyway (a real LookupSeries call is still needed either way), so there's
// no reason to duplicate the parsing on the client too.
func extractSeriesMBID(input string) (string, error) {
	input = strings.TrimSpace(input)
	if m := seriesURLPattern.FindStringSubmatch(input); m != nil {
		return strings.ToLower(m[1]), nil
	}
	if m := bareMBIDPattern.FindStringSubmatch(input); m != nil {
		return strings.ToLower(m[1]), nil
	}
	return "", fmt.Errorf("doesn't look like a MusicBrainz series link or ID")
}

// handleAddMusicSeries adds a MusicBrainz Series as a synthetic library
// "artist" (see musiclibrary.Artist.Kind's own doc comment) — CantiNode's
// second way to add music beyond one real artist at a time. Behaves like
// monitoring a real artist from here on: SetArtistMonitored, discography
// cached straight into Missing, a manual "Refresh metadata" re-syncs it
// later (handleRefreshMusicArtist's own kind branch) — no background timer
// of its own, same as a real artist has none today.
func (s *server) handleAddMusicSeries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		writeError(w, http.StatusBadRequest, "a MusicBrainz series link or ID is required")
		return
	}
	mbid, err := extractSeriesMBID(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := s.artistRefreshCtx(r)
	defer cancel()

	series, err := s.mb.LookupSeries(ctx, mbid)
	if err != nil {
		if errors.Is(err, musicbrainz.ErrSeriesHasNoReleaseGroups) {
			writeError(w, http.StatusBadRequest,
				"this MusicBrainz series has no albums CantiNode can track (it may link recordings, works, or events instead of release groups/releases)")
			return
		}
		writeError(w, http.StatusBadGateway, "look up series: "+err.Error())
		return
	}

	a, err := s.musicStore.GetOrCreateSeriesArtist(mbid, series.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.musicStore.SetArtistMonitored(a.ID, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.cacheSeriesDiscography(ctx, a.ID, series); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a, err = s.musicStore.GetArtist(a.ID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// cacheSeriesDiscography stores series' release-group membership as
// artistID's own discography — the series-add counterpart to
// cacheArtistDiscography. Delegates to internal/discography (shared with
// the periodic discoveryrefresh sweep), then hands off to
// metadataBackfill.CacheDiscographyVersions for per-release-group version/
// tracklist pre-warming — that part is genuinely kind-agnostic and lives in
// internal/metadatabackfill precisely because discography.Service
// deliberately never does it (see Refresh's own doc comment on why the
// scheduled sweep must stay cheap).
func (s *server) cacheSeriesDiscography(ctx context.Context, artistID int64, series *musicbrainz.Series) error {
	groups, err := s.discography.RefreshSeries(ctx, artistID, series)
	if err != nil {
		return err
	}
	go s.metadataBackfill.CacheDiscographyVersions(context.Background(), groups)
	return nil
}

// refreshMusicSeriesMetadata re-syncs artistID's discography from its
// series — the series-add counterpart to metadataBackfill.RefreshArtist,
// used by handleRefreshMusicArtist's own kind branch. Never touches
// bio/photo, unlike the real-artist refresh path: a series has neither.
func (s *server) refreshMusicSeriesMetadata(ctx context.Context, artistID int64, mbid string) error {
	series, err := s.mb.LookupSeries(ctx, mbid)
	if err != nil {
		return err
	}
	return s.cacheSeriesDiscography(ctx, artistID, series)
}

func (s *server) handleUnmonitorMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicStore.SetArtistMonitored(id, false); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefreshMusicArtist re-fetches an artist's cached discography and
// bio/image — the artist page's "Refresh metadata" button. Works whether
// or not the artist is currently monitored.
func (s *server) handleRefreshMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := s.musicStore.GetArtist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	ctx, cancel := s.artistRefreshCtx(r)
	defer cancel()
	refresh := s.metadataBackfill.RefreshArtist
	if a.Kind == "series" {
		refresh = s.refreshMusicSeriesMetadata
	}
	if err := refresh(ctx, id, a.MBID); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshMusicArtistMetadata and cacheFullArtistMetadata moved to
// internal/metadatabackfill (RefreshArtist/CacheFullArtistMetadata) — see
// that package's own doc comment for why: the same logic now also backs a
// periodic restart-safe sweep, not just these on-demand call sites.

// cacheArtistDiscography stores mbArtist's full release-group discography
// (any primary/secondary type — the Missing section lets the user pick,
// fully paginated via BrowseArtistReleaseGroups rather than the
// truncated-at-25 list a plain artist lookup returns) plus the
// genres/tags/rating that came back with the same lookup for free — the
// baseline every "add or refresh an artist" path needs, and the one
// handleQuickAddMusicArtist stops at. Delegates to internal/discography,
// shared with the periodic discoveryrefresh sweep.
func (s *server) cacheArtistDiscography(ctx context.Context, artistID int64, mbArtist *musicbrainz.Artist) ([]musiclibrary.ReleaseGroupCache, error) {
	return s.discography.RefreshArtist(ctx, artistID, mbArtist)
}

func (s *server) handleListMissingMusicReleaseGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	groups, err := s.musicStore.ListMissingArtistReleaseGroups(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// releaseGroupTrack is one flattened track in a releaseGroupTracklist —
// disc-relative position folded together with its medium's disc number,
// since a preview tracklist has no local track-file rows to slot into.
type releaseGroupTrack struct {
	DiscNumber    int    `json:"discNumber"`
	Position      int    `json:"position"`
	Title         string `json:"title"`
	DurationMs    int    `json:"durationMs"`
	RecordingMBID string `json:"recordingMbid"`
}

type releaseGroupTracklist struct {
	ReleaseMBID  string              `json:"releaseMbid"`
	ReleaseTitle string              `json:"releaseTitle"`
	Tracks       []releaseGroupTrack `json:"tracks"`
}

// pickRepresentativeRelease, cacheReleaseGroupVersions,
// cacheAllVersionTracklists, cacheDiscographyVersions, and storeTracklist
// all moved to internal/metadatabackfill (exported, same names minus the
// lowercase-first-letter) — see that package's own doc comment.

// backfillReleaseGroupVersions catches artists that predate release-version
// caching — added (or last refreshed) before this feature existed, so
// their discography was synced under the old single-tracklist scheme and
// has zero release_group_versions rows for any of their release groups.
// Runs alongside the metadata backfill sweep on every scan (backgrounded
// by the caller, since a large library's full backlog can take minutes to
// hours to clear at MusicBrainz's rate limit); naturally idempotent
// (CacheDiscographyVersions skips whatever's already cached, and
// ReleaseGroupMBIDsWithRealVersions — one batched query per artist rather
// than one per release group — makes an already-caught-up library cheap
// to re-check on every subsequent scan), so an interrupted sweep just
// picks up where it left off next time. Best-effort per artist.
func (s *server) backfillReleaseGroupVersions(ctx context.Context) {
	artists, err := s.musicStore.ListArtists()
	if err != nil {
		slog.Warn("music: listing artists for version backfill", "error", err)
		return
	}
	for _, a := range artists {
		if ctx.Err() != nil {
			return
		}
		groups, err := s.musicStore.ListArtistReleaseGroups(a.ID)
		if err != nil {
			slog.Warn("music: listing release groups for version backfill", "artist", a.Name, "error", err)
			continue
		}
		if len(groups) == 0 {
			continue
		}
		mbids := make([]string, len(groups))
		for i, g := range groups {
			mbids[i] = g.ReleaseGroupMBID
		}
		cached, err := s.musicStore.ReleaseGroupMBIDsWithRealVersions(mbids)
		if err != nil {
			slog.Warn("music: checking version cache", "artist", a.Name, "error", err)
			continue
		}
		var pending []musiclibrary.ReleaseGroupCache
		for _, g := range groups {
			if !cached[g.ReleaseGroupMBID] {
				pending = append(pending, g)
			}
		}
		if len(pending) > 0 {
			s.metadataBackfill.CacheDiscographyVersions(ctx, pending)
		}
	}
}

// getReleaseWithTracklist returns releaseMBID's full tracklist, from cache
// if present, otherwise fetching (and caching) it live from MusicBrainz —
// the lazy-fallback path for a version whose tracklist wasn't eagerly
// warmed yet (predates this feature, or the eager sweep hasn't reached it
// yet).
func (s *server) getReleaseWithTracklist(ctx context.Context, releaseMBID, releaseGroupMBID string) (*musicbrainz.ReleaseWithTracklist, error) {
	if cached, err := s.musicStore.GetCachedTracklist(releaseMBID); err == nil {
		var full musicbrainz.ReleaseWithTracklist
		if jerr := json.Unmarshal([]byte(cached.TracksJSON), &full); jerr == nil {
			return &full, nil
		} else {
			slog.Warn("music: cached tracklist is unreadable, refetching", "release", releaseMBID, "error", jerr)
		}
	} else if !errors.Is(err, musiclibrary.ErrNotFound) {
		return nil, err
	}
	full, err := s.mb.LookupReleaseWithTracklist(ctx, releaseMBID)
	if err != nil {
		return nil, err
	}
	if err := s.metadataBackfill.StoreTracklist(releaseGroupMBID, full); err != nil {
		slog.Warn("music: caching tracklist", "release", releaseMBID, "error", err)
	}
	return full, nil
}

// resolveRepresentativeRelease returns the full tracklist of
// releaseGroupMBID's representative release (see
// metadatabackfill.pickRepresentativeRelease) — from the cached version
// list/tracklist when available (the normal case: an artist's discography
// sync already warmed both), falling back to a live browse+fetch (and
// caching the result) for a release group nothing has cached yet. Shared by
// fetchAndCacheTracklist (the Missing/Wanted tracklist preview) and
// handleSuggestTrackFileMatches when the caller hasn't picked a specific
// version.
func (s *server) resolveRepresentativeRelease(ctx context.Context, releaseGroupMBID string) (*musicbrainz.ReleaseWithTracklist, error) {
	v, err := s.musicStore.GetRepresentativeReleaseVersion(releaseGroupMBID)
	if errors.Is(err, musiclibrary.ErrNotFound) {
		if _, cerr := s.metadataBackfill.CacheReleaseGroupVersions(ctx, releaseGroupMBID); cerr != nil {
			return nil, cerr
		}
		v, err = s.musicStore.GetRepresentativeReleaseVersion(releaseGroupMBID)
	}
	if err != nil {
		return nil, err
	}
	return s.getReleaseWithTracklist(ctx, v.ReleaseMBID, releaseGroupMBID)
}

// fetchAndCacheTracklist previews releaseGroupMBID's tracklist — the
// Missing/Wanted sections' "see the tracks" action. Flattens whichever
// full release resolveRepresentativeRelease resolves (cached, in the
// normal case) into the UI's own lightweight per-track DTO.
func (s *server) fetchAndCacheTracklist(ctx context.Context, releaseGroupMBID string) (releaseGroupTracklist, error) {
	full, err := s.resolveRepresentativeRelease(ctx, releaseGroupMBID)
	if err != nil {
		return releaseGroupTracklist{}, err
	}
	return flattenForPreview(full), nil
}

// flattenForPreview reshapes a full MusicBrainz release into the
// tracklist-preview response DTO.
func flattenForPreview(full *musicbrainz.ReleaseWithTracklist) releaseGroupTracklist {
	out := releaseGroupTracklist{ReleaseMBID: full.ID, ReleaseTitle: full.Title, Tracks: []releaseGroupTrack{}}
	for _, medium := range full.Media {
		for _, t := range medium.Tracks {
			out.Tracks = append(out.Tracks, releaseGroupTrack{
				DiscNumber:    medium.Position,
				Position:      t.Position,
				Title:         t.Title,
				DurationMs:    t.Length,
				RecordingMBID: t.Recording.ID,
			})
		}
	}
	return out
}

// handleGetReleaseGroupTracklist serves a release group's tracklist
// preview — the Missing/Wanted sections' "see the tracks" action. In the
// normal case this is served entirely from the cache resolveRepresentativeRelease
// reads (see metadataBackfill.CacheDiscographyVersions): by the time an album is visible in
// Missing/Wanted at all, its artist's discography sync has already eagerly
// cached every release group's versions and tracklists in the background.
// The live MusicBrainz fetch inside resolveRepresentativeRelease only runs
// as a fallback — e.g. a release group added to MusicBrainz's catalog
// after this artist's last sync — so it's never the expected path.
func (s *server) handleGetReleaseGroupTracklist(w http.ResponseWriter, r *http.Request) {
	mbid := r.PathValue("mbid")
	if mbid == "" {
		writeError(w, http.StatusBadRequest, "invalid release group mbid")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	out, err := s.fetchAndCacheTracklist(ctx, mbid)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleListReleaseGroupVersions serves every cached version/edition of a
// release group — the matching UI's version picker. Falls back to a live
// browse+cache (like resolveRepresentativeRelease) if nothing's cached yet
// for this release group at all.
func (s *server) handleListReleaseGroupVersions(w http.ResponseWriter, r *http.Request) {
	mbid := r.PathValue("mbid")
	if mbid == "" {
		writeError(w, http.StatusBadRequest, "invalid release group mbid")
		return
	}
	versions, err := s.musicStore.ListReleaseGroupVersions(mbid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hasRealVersionMetadata(versions) {
		ctx, cancel := s.metadataCtx(r)
		defer cancel()
		versions, err = s.metadataBackfill.CacheReleaseGroupVersions(ctx, mbid)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, versions)
}

// hasRealVersionMetadata reports whether versions contains at least one
// genuinely fetched row — not just the placeholder migration 022 carried
// over from the old single-release-tracklist scheme (release_mbid/title
// only, every other field left blank). An empty slice AND a slice
// containing only migrated placeholders both mean "go fetch the real
// list" — see musiclibrary.Store.HasReleaseGroupVersions, which this
// mirrors for the in-memory slice this handler already has in hand rather
// than issuing a second query.
func hasRealVersionMetadata(versions []musiclibrary.ReleaseGroupVersion) bool {
	for _, v := range versions {
		if v.Fetched {
			return true
		}
	}
	return false
}

// handleRemoveMusicArtist detaches (unlinks, per DeleteArtist's own FK
// warning) every track file the artist owns before deleting the row —
// optionally also deleting those files from disk. Also purges every piece
// of cached metadata this artist's release groups pulled in
// (release_group_versions, release_tracklist_cache, on-disk cover art, the
// artist's own cached photo) — none of that cascades away with the artist
// row itself (see purgeArtistCaches), so an artist removed from the
// library shouldn't leave its metadata behind; contrast handleRemoveMusicAlbum,
// which deliberately leaves all of this alone since the artist (and the
// rest of its cached discography) is still in the library.
func (s *server) handleRemoveMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)

	// Snapshot everything purgeArtistCaches needs before DeleteArtist
	// cascades artist_release_groups away.
	artist, aerr := s.musicStore.GetArtist(id)
	if aerr != nil {
		writeMusicStoreError(w, aerr)
		return
	}
	groups, gerr := s.musicStore.ListArtistReleaseGroups(id)
	if gerr != nil {
		writeError(w, http.StatusInternalServerError, gerr.Error())
		return
	}
	// Also snapshot owned albums' own specific release MBIDs — purely
	// artist-scoped (albums.artist_id cascades away with the artist row
	// itself, no cross-artist sharing concern the way a cached release
	// group can have), so their cover art must always be purged
	// regardless of whether release_group_versions has caught up with
	// this release group yet (see purgeArtistCaches).
	albums, alerr := s.musicStore.ListAlbumsByArtist(id)
	if alerr != nil {
		writeError(w, http.StatusInternalServerError, alerr.Error())
		return
	}

	// Cancel any grab still in flight for one of this artist's wanted
	// albums before DeleteArtist cascades those rows away — otherwise a
	// download that finishes after removal silently imports anyway
	// (musicscanner recreates the artist from the imported files' own
	// tags/MBIDs), resurrecting exactly what was just removed.
	if wanted, werr := s.musicStore.ListWantedAlbumsByArtist(id); werr == nil {
		s.cancelInFlightGrabs(downloadingWantedIDs(wanted), "artist removed")
	}

	files, err := s.musicStore.ListTrackFilesByArtist(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var paths []string
	for _, f := range files {
		if deleteFiles {
			paths = append(paths, f.Path)
		}
		if err := s.musicStore.SetTrackFileMatch(f.ID, nil, musiclibrary.StatusUnmatched, 0); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.musicStore.DeleteArtist(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	s.purgeArtistCaches(*artist, groups, albums)
	s.writeDeleteResult(w, deleteFiles, paths)
}

// purgeArtistCaches removes every piece of metadata cached on behalf of an
// artist that's just been deleted — release_group_versions/
// release_tracklist_cache rows (keyed globally by release-group/release
// MBID, no FK to artists, so DeleteArtist's own cascade never touches
// them), the on-disk cover art cached for each of those releases, and the
// artist's own cached photo. Best-effort throughout: a cache-purge failure
// is logged, not surfaced — the artist itself is already gone by the time
// this runs, and a leftover cache file is a cosmetic disk-space concern,
// not a correctness one.
//
// Only purges release groups no longer referenced by any remaining artist
// (DeleteArtist has already cascaded this artist's own artist_release_groups
// rows away by the time this runs, so anything still turning up belongs to
// someone else) — a release group can legitimately be cached under more than
// one artist, and wiping a still-monitored artist's cached version list,
// tracklist, and cover art just because a different artist that also
// referenced it was removed would be a silent data-loss bug. Owned albums'
// own cover art is different: albums.artist_id is purely artist-scoped (no
// cross-artist sharing possible the way a cached release group has), so
// their release MBIDs — snapshotted by the caller before DeleteArtist
// cascades the albums rows away — are always purged, regardless of whether
// release_group_versions happened to have caught up with that release
// group yet. Without this, an artist removed before its background
// discography-version sweep finished (which can take minutes to hours —
// see metadataBackfill.CacheDiscographyVersions) could have its owned albums' cover art
// survive the removal, contradicting this function's own guarantee.
func (s *server) purgeArtistCaches(artist musiclibrary.Artist, groups []musiclibrary.ReleaseGroupCache, ownedAlbums []musiclibrary.Album) {
	var candidateMBIDs []string
	for _, g := range groups {
		candidateMBIDs = append(candidateMBIDs, g.ReleaseGroupMBID)
	}
	stillReferenced, err := s.musicStore.ReleaseGroupMBIDsStillReferenced(candidateMBIDs)
	if err != nil {
		// Can't tell what's safe to purge — err on the side of leaving the
		// cache alone (a cosmetic leftover) rather than risking a wipe of
		// data a still-monitored artist depends on.
		slog.Warn("music: checking shared release groups before purge, skipping purge", "artist", artist.Name, "error", err)
		return
	}

	seenRelease := map[string]bool{}
	var releaseMBIDs, releaseGroupMBIDs []string
	addRelease := func(mbid string) {
		if mbid != "" && !seenRelease[mbid] {
			seenRelease[mbid] = true
			releaseMBIDs = append(releaseMBIDs, mbid)
		}
	}
	for _, a := range ownedAlbums {
		addRelease(a.MBID)
	}
	for _, g := range groups {
		if !stillReferenced[g.ReleaseGroupMBID] {
			releaseGroupMBIDs = append(releaseGroupMBIDs, g.ReleaseGroupMBID)
		}
	}
	versionsByGroup, err := s.musicStore.ListReleaseGroupVersionsBulk(releaseGroupMBIDs)
	if err != nil {
		slog.Warn("music: listing release group versions before purge", "artist", artist.Name, "error", err)
	} else {
		for _, versions := range versionsByGroup {
			for _, v := range versions {
				addRelease(v.ReleaseMBID)
			}
		}
	}
	if err := s.musicStore.DeleteReleaseGroupCache(releaseGroupMBIDs); err != nil {
		slog.Warn("music: purging release group cache after artist removal", "artist", artist.Name, "error", err)
	}
	for _, rmbid := range releaseMBIDs {
		if err := s.coverart.DeleteCached(rmbid); err != nil {
			slog.Warn("music: purging cover art after artist removal", "release", rmbid, "error", err)
		}
	}
	if artist.ImageURL != "" {
		if err := s.images.Delete(artist.ImageURL); err != nil {
			slog.Warn("music: purging artist image after removal", "artist", artist.Name, "error", err)
		}
	}
}

// handleRemoveMusicAlbum is handleRemoveMusicArtist's single-album
// counterpart — removes just this album (and its tracks), leaving the
// artist and its other albums untouched.
func (s *server) handleRemoveMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)

	// Same in-flight-grab cancellation as handleRemoveMusicArtist, scoped to
	// just this album's own release group — a wanted_albums row is
	// identified by (artist, release group), not by album id, so the
	// artist's other wanted albums must be filtered out.
	if album, aerr := s.musicStore.GetAlbum(id); aerr == nil {
		if wanted, werr := s.musicStore.ListWantedAlbumsByArtist(album.ArtistID); werr == nil {
			var forThisAlbum []musiclibrary.WantedAlbum
			for _, w := range wanted {
				if w.ReleaseGroupMBID == album.ReleaseGroupMBID {
					forThisAlbum = append(forThisAlbum, w)
				}
			}
			s.cancelInFlightGrabs(downloadingWantedIDs(forThisAlbum), "album removed")
		}
	}

	files, err := s.musicStore.ListTrackFilesByAlbum(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var paths []string
	for _, f := range files {
		if deleteFiles {
			paths = append(paths, f.Path)
		}
		if err := s.musicStore.SetTrackFileMatch(f.ID, nil, musiclibrary.StatusUnmatched, 0); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.musicStore.DeleteAlbum(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	s.writeDeleteResult(w, deleteFiles, paths)
}

// downloadingWantedIDs filters wanted albums down to the ones with an
// actual grab in flight (status=downloading) — the only ones
// cancelInFlightGrabs has anything to do for.
func downloadingWantedIDs(wanted []musiclibrary.WantedAlbum) []int64 {
	var ids []int64
	for _, w := range wanted {
		if w.Status == musiclibrary.WantedStatusDownloading {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

// cancelInFlightGrabs resolves any pending (status=grabbed) download tied
// to one of wantedAlbumIDs as failed, recording reason — best-effort and
// silent about it (a removal shouldn't fail or get noisy over a grab
// bookkeeping detail). Does not touch the download client itself; the
// download keeps running there and can still be removed from Activity like
// any other, this only stops CantiNode from importing it once it finishes.
func (s *server) cancelInFlightGrabs(wantedAlbumIDs []int64, reason string) {
	if len(wantedAlbumIDs) == 0 {
		return
	}
	pending, err := s.downloads.Store().ListGrabsForWantedAlbums(wantedAlbumIDs, download.GrabStatusGrabbed)
	if err != nil {
		slog.Error("music: list in-flight grabs before removal", "error", err)
		return
	}
	for _, g := range pending {
		if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed, reason); err != nil {
			slog.Error("music: cancel in-flight grab", "grab_id", g.ID, "error", err)
		}
	}
}

// writeDeleteResult is the artist/album-remove response shape both
// handlers share: always 200, "deleted": true, plus a per-path fileErrors
// list only when deleteFiles was actually requested and something failed —
// deliberately not internal/api's own finishDelete (204 when deleteFiles is
// false), since these two endpoints' 200-always contract predates that
// helper and frontend/tests already depend on it.
func (s *server) writeDeleteResult(w http.ResponseWriter, deleteFiles bool, paths []string) {
	if deleteFiles {
		if _, errs := s.removeFilesFromDisk(paths); len(errs) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "fileErrors": errs})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// --- Albums / tracks ---

func (s *server) handleListMusicAlbumsByArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	albums, err := s.musicStore.ListAlbumsByArtist(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, albums)
}

func (s *server) handleGetMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	album, err := s.musicStore.GetAlbum(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, album)
}

func (s *server) handleListMusicTracksByAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tracks, err := s.musicStore.ListTracksByAlbum(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tracks)
}

// handleMusicAlbumCover serves an album's cached front cover image,
// fetching and caching it from Cover Art Archive on first request. 404 both
// when the album doesn't exist and when it has no cover art, so a broken-
// image icon is the only visible difference.
func (s *server) handleMusicAlbumCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	album, err := s.musicStore.GetAlbum(id)
	if err != nil {
		w.WriteHeader(musicNotFoundStatus(err))
		return
	}
	if album.MBID == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	path, contentType, err := s.coverart.GetFrontCover(r.Context(), album.ReleaseGroupMBID, album.MBID)
	if err != nil {
		if errors.Is(err, coverart.ErrNoCoverArt) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	// Cover Art Archive content never changes for a given release MBID once
	// cached, so this is safe for the browser to cache indefinitely.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, path)
}

// handleAudioDBAlbumLink redirects to an album's own page on
// theaudiodb.com — the album page's "TheAudioDB" link icon. Unlike
// MusicBrainz (whose browsable URLs are MBID-based, so the frontend
// builds those links directly with no backend involvement — see
// musicbrainz.org/release-group/{mbid}), TheAudioDB's own site URLs use
// its internal numeric album id, which CantiNode has no other reason to
// look up or persist anywhere. Rather than storing it (a schema change
// and a backfill sweep for a value nothing else needs), this looks it up
// live, on demand, only the moment someone actually clicks the icon —
// never on a plain page load. 404s when TheAudioDB has no entry for this
// release group at all, or no idAlbum on the entry it does have.
func (s *server) handleAudioDBAlbumLink(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	album, err := s.musicStore.GetAlbum(id)
	if err != nil {
		w.WriteHeader(musicNotFoundStatus(err))
		return
	}
	if album.ReleaseGroupMBID == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	meta, err := s.audiodb.LookupAlbumByReleaseGroupMBID(r.Context(), album.ReleaseGroupMBID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if meta == nil || meta.IDAlbum == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "https://www.theaudiodb.com/album/"+meta.IDAlbum, http.StatusFound)
}

// handleReleaseGroupCover serves a release group's front cover art via its
// cached representative release — the Missing/Wanted grid's counterpart to
// handleMusicAlbumCover, for an album with no owned files (and so no
// albums row/specific release mbid of its own) yet. Falls back to a live
// browse+cache the same way resolveRepresentativeRelease does when nothing
// is cached for this release group yet.
func (s *server) handleReleaseGroupCover(w http.ResponseWriter, r *http.Request) {
	mbid := r.PathValue("mbid")
	if mbid == "" {
		writeError(w, http.StatusBadRequest, "invalid release group mbid")
		return
	}
	v, err := s.musicStore.GetRepresentativeReleaseVersion(mbid)
	if errors.Is(err, musiclibrary.ErrNotFound) {
		ctx, cancel := s.metadataCtx(r)
		defer cancel()
		if _, cerr := s.metadataBackfill.CacheReleaseGroupVersions(ctx, mbid); cerr != nil {
			writeError(w, http.StatusBadGateway, cerr.Error())
			return
		}
		v, err = s.musicStore.GetRepresentativeReleaseVersion(mbid)
	}
	if err != nil {
		w.WriteHeader(musicNotFoundStatus(err))
		return
	}

	path, contentType, err := s.coverart.GetFrontCover(r.Context(), mbid, v.ReleaseMBID)
	if err != nil {
		if errors.Is(err, coverart.ErrNoCoverArt) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, path)
}

func (s *server) handleListMusicTrackFilesByTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	files, err := s.musicStore.ListTrackFilesByTrack(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *server) handleListUnmatchedTrackFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.musicScanner.ListUnmatchedWithGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// --- Manual matching / organizing / tag-writing ---

func (s *server) handleSearchMusicBrainzRecordings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	results, err := s.musicScanner.SearchMusicBrainz(ctx, q.Get("artist"), q.Get("album"), q.Get("title"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// handleSuggestTrackFileMatches is the unmatched-files page's auto-match
// action: given a batch of unmatched files (normally everything in one
// folder — see UnmatchedFilesView's own grouping) and a release group the
// user picked from their own artist's wanted/missing albums (not a fresh
// MusicBrainz search — the whole point is reusing what's already in their
// library), proposes which track within that release each file's own
// tags best correspond to. ReleaseMBID optionally names a specific cached
// version/edition (from the matching UI's version picker) to slot against,
// via getReleaseWithTracklist, instead of the release group's default
// representative release (resolveRepresentativeRelease). Nothing is
// applied here — every suggestion
// still needs its own POST to /music/trackfile/{id}/match to actually
// commit, same as a manual match, so the human reviewing always approves
// before anything changes.
func (s *server) handleSuggestTrackFileMatches(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileIDs          []int64 `json:"fileIds"`
		ReleaseGroupMBID string  `json:"releaseGroupMbid"`
		ReleaseMBID      string  `json:"releaseMbid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.FileIDs) == 0 || req.ReleaseGroupMBID == "" {
		writeError(w, http.StatusBadRequest, "fileIds and releaseGroupMbid are required")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	var release *musicbrainz.ReleaseWithTracklist
	var err error
	if req.ReleaseMBID != "" {
		release, err = s.getReleaseWithTracklist(ctx, req.ReleaseMBID, req.ReleaseGroupMBID)
	} else {
		release, err = s.resolveRepresentativeRelease(ctx, req.ReleaseGroupMBID)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	suggestions := s.musicScanner.SuggestMatches(req.FileIDs, release)
	writeJSON(w, http.StatusOK, map[string]any{"releaseTitle": release.Title, "suggestions": suggestions})
}

func (s *server) handleManualMatchTrackFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		RecordingMBID string `json:"recordingMbid"`
		ReleaseMBID   string `json:"releaseMbid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RecordingMBID == "" {
		writeError(w, http.StatusBadRequest, "recordingMbid is required")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	if err := s.musicScanner.ManualMatch(ctx, id, req.RecordingMBID, req.ReleaseMBID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tf, err := s.musicStore.GetTrackFile(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tf)
}

func (s *server) handleClearTrackFileMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicScanner.ClearMatch(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteTrackFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicScanner.DeleteTrackFile(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handlePreviewOrganizeTrackFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	path, err := s.musicScanner.PlanOrganizePath(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *server) handleOrganizeTrackFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	path, err := s.musicScanner.OrganizeFile(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *server) handleWriteMusicTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicScanner.WriteTags(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleWriteMusicTagsForAlbum writes every matched file in an album back
// to its own tags — the album page's own bulk "Write tags" action, the
// album-scoped counterpart to handleOrganizeMusicAlbum.
func (s *server) handleWriteMusicTagsForAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	written, errs, err := s.musicScanner.WriteTagsForAlbum(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"written": written, "errors": errs})
}

// handleWriteMusicTagsForArtist is handleWriteMusicTagsForAlbum scoped to
// every album an artist owns — the artist page's own bulk "Write tags"
// action.
func (s *server) handleWriteMusicTagsForArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	written, errs, err := s.musicScanner.WriteTagsForArtist(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"written": written, "errors": errs})
}

func (s *server) handlePreviewOrganizeMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	moves, err := s.musicScanner.PlanOrganizeArtist(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moves": moves})
}

func (s *server) handleOrganizeMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	moves, errs, err := s.musicScanner.OrganizeArtist(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moves": moves, "errors": errs})
}

// handlePreviewMoveMusicArtist previews the artist page's "Move to
// root folder…" action — every file that would relocate to the given
// root folder, and their total size, so the confirm dialog can warn
// concretely ("14 files, 2.1 GB will move to Archive Drive") before the
// user approves anything.
func (s *server) handlePreviewMoveMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	destID, err := strconv.ParseInt(r.URL.Query().Get("rootFolderId"), 10, 64)
	if err != nil || destID <= 0 {
		writeError(w, http.StatusBadRequest, "rootFolderId is required")
		return
	}
	// PlanMoveArtist itself never checks artist existence — ListTrackFilesByArtist
	// just returns an empty slice for a nonexistent id, which without this
	// check would look identical to "this artist owns nothing to move" (a
	// perfectly valid 200) rather than the 404 a bad id should actually get.
	if _, err := s.musicStore.GetArtist(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	moves, err := s.musicScanner.PlanMoveArtist(id, destID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var totalBytes int64
	for _, m := range moves {
		totalBytes += m.SizeBytes
	}
	writeJSON(w, http.StatusOK, map[string]any{"moves": moves, "totalBytes": totalBytes})
}

// musicMoveState is the last (or currently running) artist move's status,
// reported by GET /api/v1/music/move/status — mirrors musicScanState's
// own shape and locking convention (see handleTriggerMusicScan).
type musicMoveState struct {
	Running          bool                      `json:"running"`
	ArtistID         int64                     `json:"artistId,omitempty"`
	ArtistName       string                    `json:"artistName,omitempty"`
	DestRootFolderID int64                     `json:"destRootFolderId,omitempty"`
	StartedAt        *time.Time                `json:"startedAt,omitempty"`
	FinishedAt       *time.Time                `json:"finishedAt,omitempty"`
	Moved            []musicscanner.ArtistMove `json:"moved,omitempty"`
	Errors           []string                  `json:"errors,omitempty"`
	Error            string                    `json:"error,omitempty"`
}

// handleMoveMusicArtist starts moving an artist's whole discography to a
// different root folder in the background and returns immediately — a
// large library can mean copying many GB, sometimes across physical
// drives (a real copy, not a fast same-drive rename — see
// musicscanner.MoveArtist). Poll GET /api/v1/music/move/status for
// progress. Refuses to start a second move while one is already running,
// the same one-at-a-time rule handleTriggerMusicScan already applies to
// scans.
func (s *server) handleMoveMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		RootFolderID int64 `json:"rootFolderId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RootFolderID <= 0 {
		writeError(w, http.StatusBadRequest, "rootFolderId is required")
		return
	}
	artist, err := s.musicStore.GetArtist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	if _, err := s.musicStore.GetRootFolder(req.RootFolderID); err != nil {
		writeMusicStoreError(w, err)
		return
	}

	// See handleTriggerMusicScan's own doc comment for why a scan and a
	// move can't be allowed to run at the same time.
	s.musicScanMu.Lock()
	scanRunning := s.musicScanState.Running
	s.musicScanMu.Unlock()
	if scanRunning {
		writeError(w, http.StatusConflict, "a music scan is in progress — try again once it finishes")
		return
	}

	s.musicMoveMu.Lock()
	if s.musicMoveState.Running {
		s.musicMoveMu.Unlock()
		writeError(w, http.StatusConflict, "a move is already running")
		return
	}
	now := time.Now().UTC()
	s.musicMoveState = musicMoveState{
		Running: true, ArtistID: id, ArtistName: artist.Name,
		DestRootFolderID: req.RootFolderID, StartedAt: &now,
	}
	s.musicMoveMu.Unlock()

	go func() {
		// The request's own context is canceled the moment the handler
		// returns — long before a real, potentially large cross-drive
		// copy could finish.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		moved, errs, err := s.musicScanner.MoveArtist(ctx, id, req.RootFolderID)

		s.musicMoveMu.Lock()
		finished := time.Now().UTC()
		s.musicMoveState.Running = false
		s.musicMoveState.FinishedAt = &finished
		s.musicMoveState.Moved = moved
		s.musicMoveState.Errors = errs
		if err != nil {
			s.musicMoveState.Error = err.Error()
		}
		s.musicMoveMu.Unlock()
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *server) handleMusicMoveStatus(w http.ResponseWriter, r *http.Request) {
	s.musicMoveMu.Lock()
	state := s.musicMoveState
	s.musicMoveMu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (s *server) handlePreviewOrganizeMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	moves, err := s.musicScanner.PlanOrganizeAlbum(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moves": moves})
}

func (s *server) handleOrganizeMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	moves, errs, err := s.musicScanner.OrganizeAlbum(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moves": moves, "errors": errs})
}

// --- Scan control ---

// musicScanState is the last (or currently running) music scan's status,
// reported by GET /api/v1/music/scan/status.
type musicScanState struct {
	Running    bool                     `json:"running"`
	StartedAt  *time.Time               `json:"startedAt,omitempty"`
	FinishedAt *time.Time               `json:"finishedAt,omitempty"`
	Result     *musicscanner.ScanResult `json:"result,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

// handleTriggerMusicScan starts a full music scan (every music root folder)
// in the background and returns immediately — MusicBrainz is rate-limited
// to about 1 request/sec, so a library with hundreds of unmatched files
// takes minutes, not seconds. Poll GET /api/v1/music/scan/status for
// progress. Refuses to start a second scan while one is already running,
// or while an artist move is running: both touch the same track_files
// rows (path, root_folder_id) for files potentially in flight between
// root folders, and a scan's DeleteTrackFilesMissing racing a move's
// SetTrackFileLocation can lose or duplicate a row. Checking the move
// state right before starting narrows that window rather than closing it
// outright — the same "collapse, don't eliminate" mitigation
// internal/importer's stillGrabbed already uses for its own analogous race.
func (s *server) handleTriggerMusicScan(w http.ResponseWriter, r *http.Request) {
	s.musicMoveMu.Lock()
	moveRunning := s.musicMoveState.Running
	s.musicMoveMu.Unlock()
	if moveRunning {
		writeError(w, http.StatusConflict, "an artist move is in progress — try again once it finishes")
		return
	}

	s.musicScanMu.Lock()
	if s.musicScanState.Running {
		s.musicScanMu.Unlock()
		writeError(w, http.StatusConflict, "a music scan is already running")
		return
	}
	now := time.Now().UTC()
	s.musicScanState = musicScanState{Running: true, StartedAt: &now}
	s.musicScanMu.Unlock()

	go func() {
		// The request's own context is canceled the moment the handler
		// returns — long before a real, rate-limited scan could finish.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		result, err := s.musicScanner.ScanAll(ctx)
		s.metadataBackfill.PollOnce(ctx)

		s.musicScanMu.Lock()
		finished := time.Now().UTC()
		s.musicScanState.Running = false
		s.musicScanState.FinishedAt = &finished
		s.musicScanState.Result = result
		if err != nil {
			s.musicScanState.Error = err.Error()
		}
		s.musicScanMu.Unlock()

		// Backgrounded separately from the scan itself, and started only
		// after Running has already flipped back to false: a large
		// library's full version-cache backlog can take minutes to hours
		// to clear at MusicBrainz's rate limit (see its own doc comment),
		// and unlike the scan proper, nothing needs to wait on it — a
		// second "Scan files" click shouldn't see a stale 409 "already
		// running" for a sweep that isn't the scan at all.
		go s.backfillReleaseGroupVersions(context.Background())
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// cacheNewArtistsMetadata (the on-scan trigger for artists a scan just
// discovered implicitly) moved to internal/metadatabackfill.Service.PollOnce
// — see handleTriggerMusicScan's own call site and that package's doc
// comment for why this now also runs on its own independent periodic timer,
// not just once per scan.

// handleScanMusicAlbum rescans a single album's own folder — the album
// page's "Scan files" action. Unlike handleTriggerMusicScan, this runs
// synchronously and isn't tracked by s.musicScanState: it walks one small
// directory rather than every root folder, so there's no need for the
// background-job/polling dance a full library scan requires.
func (s *server) handleScanMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	result, err := s.musicScanner.ScanAlbumFolder(ctx, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleMusicScanStatus(w http.ResponseWriter, r *http.Request) {
	s.musicScanMu.Lock()
	state := s.musicScanState
	s.musicScanMu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

// --- Wanted albums / acquisition ---

// handleWantMusicAlbum is the artist page's per-row/bulk "Add"/"Add &
// Monitor" action. Monitor=true additionally flips the artist's own
// IsMonitored flag on — no auto-grab either way, just marks it as tracked.
func (s *server) handleWantMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ReleaseGroupMBID string `json:"releaseGroupMbid"`
		Monitor          bool   `json:"monitor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReleaseGroupMBID == "" {
		writeError(w, http.StatusBadRequest, "releaseGroupMbid is required")
		return
	}

	groups, err := s.musicStore.ListArtistReleaseGroups(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var found *musiclibrary.ReleaseGroupCache
	for i := range groups {
		if groups[i].ReleaseGroupMBID == req.ReleaseGroupMBID {
			found = &groups[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusBadRequest,
			"release group is not in this artist's cached discography — monitor or refresh the artist first")
		return
	}
	wanted, err := s.musicStore.GetOrCreateWantedAlbum(id, found.ReleaseGroupMBID, found.Title, found.PrimaryType, found.FirstReleaseDate)
	if errors.Is(err, musiclibrary.ErrAlreadyOwned) {
		writeError(w, http.StatusBadRequest, "this album is already owned")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Monitor {
		if err := s.musicStore.SetArtistMonitored(id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, wanted)
}

func (s *server) handleListWantedMusicAlbums(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	wanted, err := s.musicStore.ListWantedAlbumsByArtist(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wanted)
}

// handleRemoveWantedMusicAlbum stops wanting an album — the "not wanted
// after all" action. This deletes the wanted_albums row outright rather
// than marking it with some "ignored" status: a status that lingers still
// counts as "wanted" for ListMissingArtistReleaseGroups's own exclusion
// check, which would strand the album in neither Missing nor Wanted.
// Deleting it is what actually lets it fall back into Missing.
func (s *server) handleRemoveWantedMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicStore.DeleteWantedAlbum(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSearchWantedMusicAlbum searches every enabled indexer for a wanted
// album — the query is the artist's name plus the album's own title, which
// in practice finds the right release across arbitrary indexer naming
// conventions far more reliably than either alone. Every candidate comes
// back scored against the default quality profile — approved and rejected
// alike, blocklisted ones dropped outright — so the UI can show the whole
// picture (score, parsed format/retail, why a rejected one was rejected)
// the way ReleaseBrowser does, rather than a stripped-down approved-only
// list with no way to see or force-grab a near-miss.
func (s *server) handleSearchWantedMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	wanted, err := s.musicStore.GetWantedAlbum(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	artist, err := s.musicStore.GetArtist(wanted.ArtistID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	query := artist.Name + " " + wanted.Title
	prefs := release.PreferencesFor(s.store, "music")
	candidates, errs, err := candidatesearch.Search(ctx, s.indexers, s.downloads, query, wanted.Title, "music", prefs, artist.SearchRelevanceName())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"releases": candidates, "errors": errs})
}

// handleGrabWantedMusicAlbum sends a release (a result from a prior
// handleSearchWantedMusicAlbum call — the caller passes back whichever one
// the user picked) to the matching download client. The grab is recorded
// against wanted.ID (GrabRelease's wantedAlbumID) so internal/importer can
// find its way back to this wanted_albums row once the download resolves
// — transitioning it to downloaded on success, or back to wanted on
// failure — instead of leaving it stuck at "downloading" forever.
func (s *server) handleGrabWantedMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Title       string `json:"title"`
		DownloadURL string `json:"downloadUrl"`
		GUID        string `json:"guid"`
		Protocol    string `json:"protocol"`
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
	wanted, err := s.musicStore.GetWantedAlbum(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	// Claim before grabbing, not after: a blind grab-then-set-status let
	// this same album be grabbed twice by two callers racing on the same
	// "still wanted" read — most realistically this endpoint firing at the
	// same moment the automatic wanted-list sweep (internal/autosearch)
	// does. The claim is a compare-and-swap (status must still be
	// 'wanted'), so only one caller ever proceeds past this point.
	claimed, err := s.musicStore.ClaimWantedAlbumForDownload(wanted.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "this album is already downloading")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	result, _, err := s.downloads.GrabRelease(ctx, req.Protocol, req.DownloadURL, req.Title, req.GUID, wanted.ID, 0, "music")
	if err != nil {
		// The claim already flipped status to "downloading" — release it
		// back to "wanted" so this isn't stuck unsearchable after a failed
		// grab attempt.
		if revertErr := s.musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusWanted); revertErr != nil {
			slog.Error("music: revert wanted album claim after failed grab", "wanted_album_id", wanted.ID, "error", revertErr)
		}
		if errors.Is(err, download.ErrNoClient) {
			writeError(w, http.StatusServiceUnavailable,
				"no enabled "+req.Protocol+" download client — add one under Settings")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": result.Client, "id": result.ID})
}

// handleSearchAlbumUpgrade searches for a better release of an
// already-owned album — the album page's manual "Search upgrade" action,
// separate from the wanted-album search above since there's no
// wanted_albums row to search against once an album is owned. Requires the
// music quality profile's UpgradesAllowed on, and only offers this at all
// when the album's own best-owned format hasn't already met the profile's
// Cutoff (empty cutoff = its single best format) — the same
// "UpgradesAllowed keeps it wanted while below Cutoff" rule
// library.QualityProfile documents, just checked here instead of kept
// alive via a lingering wanted_albums row. Every candidate comes back
// scored with MinFormatScore set to the owned format's own score, so only
// a release that's a genuine step up ever approves (see
// release.Preferences.MinFormatScore).
func (s *server) handleSearchAlbumUpgrade(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	album, upgradePrefs, ok := s.resolveAlbumUpgrade(w, id)
	if !ok {
		return
	}
	artist, err := s.musicStore.GetArtist(album.ArtistID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	query := artist.Name + " " + album.Title
	candidates, errs, err := candidatesearch.Search(ctx, s.indexers, s.downloads, query, album.Title, "music", upgradePrefs, artist.SearchRelevanceName())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"releases": candidates, "errors": errs})
}

// resolveAlbumUpgrade loads albumID and checks whether it's eligible for an
// upgrade search/grab right now — the default music quality profile must
// have upgrades allowed, the album's best owned format must be one the
// profile actually recognizes (an unrecognized format can't be compared
// against a cutoff at all; silently treating that as "no restriction" was
// a real bug), and that format must not already meet the profile's
// cutoff. Writes the appropriate error response and returns ok=false if
// any of that fails. Shared by handleSearchAlbumUpgrade and
// handleGrabAlbumUpgrade so a grab can never proceed under conditions the
// search step itself would have refused — settings can change between a
// search and a grab, or a caller can hit the grab endpoint directly
// without searching first. prefs comes back with MinFormatScore already
// set to the album's current best format score, ready to hand straight to
// candidatesearch.Search.
func (s *server) resolveAlbumUpgrade(w http.ResponseWriter, albumID int64) (album *musiclibrary.Album, prefs release.Preferences, ok bool) {
	album, err := s.musicStore.GetAlbum(albumID)
	if err != nil {
		writeMusicStoreError(w, err)
		return nil, release.Preferences{}, false
	}
	profile, err := s.store.DefaultProfile("music")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no default music quality profile configured")
		return nil, release.Preferences{}, false
	}
	if !profile.UpgradesAllowed {
		writeError(w, http.StatusBadRequest,
			`upgrades are not enabled on the music quality profile — turn on "Allow upgrades" under Settings → Quality Profiles first`)
		return nil, release.Preferences{}, false
	}

	prefs = release.PreferencesFor(s.store, "music")
	files, err := s.musicStore.ListTrackFilesByAlbum(albumID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, release.Preferences{}, false
	}
	currentBest := 0
	for _, f := range files {
		if sc, ok := prefs.FormatScores[strings.ToLower(f.Format)]; ok && sc > currentBest {
			currentBest = sc
		}
	}
	if currentBest == 0 {
		writeError(w, http.StatusBadRequest,
			"this album's current format isn't in the quality profile's format list — add it there before searching for an upgrade")
		return nil, release.Preferences{}, false
	}
	cutoffScore := 0
	if profile.Cutoff != "" {
		cutoffScore = prefs.FormatScores[strings.ToLower(profile.Cutoff)]
	} else {
		for _, sc := range prefs.FormatScores {
			if sc > cutoffScore {
				cutoffScore = sc
			}
		}
	}
	if currentBest >= cutoffScore {
		writeError(w, http.StatusBadRequest,
			"this album's format already meets the quality profile's cutoff — nothing to upgrade")
		return nil, release.Preferences{}, false
	}

	prefs.MinFormatScore = currentBest
	return album, prefs, true
}

// handleGrabAlbumUpgrade sends an upgrade candidate (from
// handleSearchAlbumUpgrade) to its download client. Unlike
// handleGrabWantedMusicAlbum, this isn't tied to a wanted_albums row
// (wantedAlbumID 0 — GrabRelease and internal/importer both already treat
// that as "no wanted album to update"): the album is already owned, so
// there's nothing to transition to "downloading". Once the download
// finishes, internal/importer copies it in and the scanner matches the new
// (better) file against this album's existing tracks — alongside the old
// file, not replacing it; removing the old one is a manual step via its
// own "delete" button on the track, the same as any other file cleanup.
func (s *server) handleGrabAlbumUpgrade(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Title       string `json:"title"`
		DownloadURL string `json:"downloadUrl"`
		GUID        string `json:"guid"`
		Protocol    string `json:"protocol"`
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
	// Re-derives the same eligibility handleSearchAlbumUpgrade already
	// checked, rather than trusting that the search step ever ran: a
	// setting can change between search and grab, and this endpoint is
	// otherwise reachable directly (a stale UI, a script, a curl call)
	// with no search step at all — without this, "Allow upgrades" being
	// off wouldn't actually stop an upgrade grab.
	if _, _, ok := s.resolveAlbumUpgrade(w, id); !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	result, _, err := s.downloads.GrabRelease(ctx, req.Protocol, req.DownloadURL, req.Title, req.GUID, 0, id, "music")
	if errors.Is(err, download.ErrNoClient) {
		writeError(w, http.StatusServiceUnavailable,
			"no enabled "+req.Protocol+" download client — add one under Settings")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": result.Client, "id": result.ID})
}

// --- Settings ---

func (s *server) handleGetMusicSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.MusicSettings())
}

func (s *server) handlePutMusicSettings(w http.ResponseWriter, r *http.Request) {
	var m config.MusicSettings
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetMusic(m); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	ns := s.cfg.NamingSettings()
	s.musicScanner.UpdateSettings(ns.MusicFile, m.MinMatchConfidence, m.OrganizeOnMatch)
	writeJSON(w, http.StatusOK, s.cfg.MusicSettings())
}
