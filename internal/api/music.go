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
	"strings"
	"time"

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

func (s *server) handleListMusicArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := s.musicStore.ListArtists()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, artists)
}

// musicArtistDetail is musiclibrary.Artist plus its owned-album count — the
// artist page's header.
type musicArtistDetail struct {
	musiclibrary.Artist
	OwnedAlbumCount int `json:"ownedAlbumCount"`
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
	writeJSON(w, http.StatusOK, musicArtistDetail{Artist: *a, OwnedAlbumCount: len(albums)})
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
	ctx, cancel := s.metadataCtx(r)
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
	if err := s.refreshMusicArtistMetadata(ctx, a.ID, req.MBID); err != nil {
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
	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	if err := s.refreshMusicArtistMetadata(ctx, id, a.MBID); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refreshMusicArtistMetadata caches mbid's entire release-group list (any
// primary/secondary type — the Missing section lets the user pick) and
// best-effort fetches bio/image from TheAudioDB. A TheAudioDB failure is
// never fatal — the MusicBrainz side alone is enough to succeed.
func (s *server) refreshMusicArtistMetadata(ctx context.Context, artistID int64, mbid string) error {
	mbArtist, err := s.mb.LookupArtist(ctx, mbid)
	if err != nil {
		return err
	}
	groups := make([]musiclibrary.ReleaseGroupCache, 0, len(mbArtist.ReleaseGroups))
	for _, rg := range mbArtist.ReleaseGroups {
		groups = append(groups, musiclibrary.ReleaseGroupCache{
			ReleaseGroupMBID: rg.ID,
			Title:            rg.Title,
			PrimaryType:      rg.PrimaryType,
			SecondaryTypes:   rg.SecondaryTypes,
			FirstReleaseDate: rg.FirstReleaseDate,
		})
	}
	if err := s.musicStore.ReplaceArtistReleaseGroups(artistID, groups); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.musicStore.SetArtistSynced(artistID, now); err != nil {
		return err
	}

	// Eagerly cache every release group's tracklist, in the background — the
	// point is that browsing Missing/Wanted afterward never calls
	// MusicBrainz at all, only this (monitor, or an explicit "Refresh
	// metadata") does. Backgrounded because each tracklist costs 2
	// MusicBrainz requests at its ~1/sec rate limit, so a prolific artist's
	// full discography can take minutes — far too long to hold this
	// request (or the scan that may have triggered it, via
	// cacheNewArtistsMetadata) open for. Detached from ctx (which dies the
	// moment this handler returns) the same way the music scan's own
	// background goroutine is.
	go s.cacheReleaseGroupTracklists(context.Background(), groups)

	meta, err := s.audiodb.LookupArtistByMBID(ctx, mbid)
	if err != nil {
		// Transient failure (network, TheAudioDB down) — cosmetic, not fatal,
		// and leaves MetadataFetchedAt unset so a later scan or explicit
		// refresh tries again rather than treating this as a permanent miss.
		return nil
	}
	bio, imageURL := "", ""
	if meta != nil {
		bio, imageURL = meta.Bio, meta.ImageURL
	}
	// Stamped even when TheAudioDB simply has nothing for this artist (a
	// definitive answer, not a failure) so cacheNewArtistsMetadata doesn't
	// re-query it on every subsequent scan.
	return s.musicStore.SetArtistMetadata(artistID, bio, imageURL, now)
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

// pickRepresentativeRelease chooses which of a release group's releases to
// show a tracklist preview for: an "Official" release over any other status
// (promos/bootlegs/pseudo-releases are frequently missing tracks or
// reordered), then the earliest dated one as a stable, deterministic
// tie-break. Returns nil for an empty slice.
func pickRepresentativeRelease(releases []musicbrainz.ReleaseSearchResult) *musicbrainz.ReleaseSearchResult {
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

// fetchAndCacheTracklist previews releaseGroupMBID's tracklist straight
// from MusicBrainz — used both by handleGetReleaseGroupTracklist's
// (unlikely) cache-miss fallback and by cacheReleaseGroupTracklists' eager
// sweep. There's no scanned file to resolve a specific release from (the
// way folder-level matching does), so it browses every release under the
// release group and picks one representative release to show (see
// pickRepresentativeRelease) rather than a track-file-backed listing.
// Always writes through to musicStore's cache on success — a tracklist
// essentially never changes once released, so it's cached forever, not on
// any TTL.
func (s *server) fetchAndCacheTracklist(ctx context.Context, releaseGroupMBID string) (releaseGroupTracklist, error) {
	releases, err := s.mb.BrowseReleaseGroupReleases(ctx, releaseGroupMBID)
	if err != nil {
		return releaseGroupTracklist{}, err
	}
	best := pickRepresentativeRelease(releases)
	if best == nil {
		return releaseGroupTracklist{}, fmt.Errorf("no releases found for this release group")
	}
	full, err := s.mb.LookupReleaseWithTracklist(ctx, best.ID)
	if err != nil {
		return releaseGroupTracklist{}, err
	}

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

	if tracksJSON, err := json.Marshal(out.Tracks); err == nil {
		if err := s.musicStore.SetCachedTracklist(releaseGroupMBID, out.ReleaseMBID, out.ReleaseTitle, string(tracksJSON)); err != nil {
			slog.Warn("caching release group tracklist", "releaseGroupMbid", releaseGroupMBID, "error", err)
		}
	}
	return out, nil
}

// cacheReleaseGroupTracklists eagerly fetches and caches every one of
// groups' tracklists not already cached — run in the background after an
// artist's discography is (re)synced (see refreshMusicArtistMetadata), so
// that afterward, browsing/expanding any of its Missing or Wanted albums
// is served entirely from musicStore's cache: MusicBrainz is only ever
// called here, by monitoring or an explicit "Refresh metadata", never by
// merely looking at the page. The same cache also backs an album once it's
// grabbed and sits in the Wanted section, and would still answer a
// tracklist request for it if ever asked again after it's owned — nothing
// keys this cache on status, only on the release group's MBID, so it
// simply follows the album across Missing/Wanted/owned unchanged. Skips
// (rather than re-fetches) anything already cached, since a tracklist
// never changes once released; best-effort per release group, so one
// failure is logged and skipped rather than aborting the rest of the sweep.
func (s *server) cacheReleaseGroupTracklists(ctx context.Context, groups []musiclibrary.ReleaseGroupCache) {
	for _, g := range groups {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.musicStore.GetCachedTracklist(g.ReleaseGroupMBID); err == nil {
			continue
		} else if !errors.Is(err, musiclibrary.ErrNotFound) {
			slog.Warn("music: checking tracklist cache", "releaseGroup", g.Title, "error", err)
			continue
		}
		if _, err := s.fetchAndCacheTracklist(ctx, g.ReleaseGroupMBID); err != nil {
			slog.Warn("music: caching tracklist", "releaseGroup", g.Title, "error", err)
		}
	}
}

// handleGetReleaseGroupTracklist serves a release group's tracklist
// preview — the Missing/Wanted sections' "see the tracks" action. In the
// normal case this is a pure cache read (see cacheReleaseGroupTracklists):
// by the time an album is visible in Missing/Wanted at all, its artist's
// discography sync has already eagerly cached every release group's
// tracklist in the background. The live MusicBrainz fetch here only runs
// as a fallback — e.g. a release group added to MusicBrainz's catalog
// after this artist's last sync — so it's never the expected path.
func (s *server) handleGetReleaseGroupTracklist(w http.ResponseWriter, r *http.Request) {
	mbid := r.PathValue("mbid")
	if mbid == "" {
		writeError(w, http.StatusBadRequest, "invalid release group mbid")
		return
	}

	if cached, err := s.musicStore.GetCachedTracklist(mbid); err == nil {
		var tracks []releaseGroupTrack
		if err := json.Unmarshal([]byte(cached.TracksJSON), &tracks); err == nil {
			writeJSON(w, http.StatusOK, releaseGroupTracklist{
				ReleaseMBID: cached.ReleaseMBID, ReleaseTitle: cached.ReleaseTitle, Tracks: tracks,
			})
			return
		}
	} else if !errors.Is(err, musiclibrary.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
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

// handleRemoveMusicArtist detaches (unlinks, per DeleteArtist's own FK
// warning) every track file the artist owns before deleting the row —
// optionally also deleting those files from disk.
func (s *server) handleRemoveMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)

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
	s.writeDeleteResult(w, deleteFiles, paths)
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
	want := make(map[int64]bool, len(wantedAlbumIDs))
	for _, id := range wantedAlbumIDs {
		want[id] = true
	}
	pending, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		slog.Error("music: list in-flight grabs before removal", "error", err)
		return
	}
	for _, g := range pending {
		if !want[g.WantedAlbumID] {
			continue
		}
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

	path, contentType, err := s.coverart.GetFrontCover(r.Context(), album.MBID)
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
	files, err := s.musicStore.ListTrackFilesByStatus(musiclibrary.StatusUnmatched)
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
// progress. Refuses to start a second scan while one is already running.
func (s *server) handleTriggerMusicScan(w http.ResponseWriter, r *http.Request) {
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
		s.cacheNewArtistsMetadata(ctx)

		s.musicScanMu.Lock()
		finished := time.Now().UTC()
		s.musicScanState.Running = false
		s.musicScanState.FinishedAt = &finished
		s.musicScanState.Result = result
		if err != nil {
			s.musicScanState.Error = err.Error()
		}
		s.musicScanMu.Unlock()
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// cacheNewArtistsMetadata fills in discography/bio/photo for artists a scan
// just discovered by matching a file (musicscanner.matchFolder/matchFile
// create the artist row directly via musicStore, with no MusicBrainz or
// TheAudioDB metadata beyond name/MBID). This is the "added" side of "cache
// everything on add, never refetch until asked": explicitly monitoring an
// artist already does this inline (see handleMonitorMusicArtist); this is
// its counterpart for artists that appear implicitly, by owning a file, and
// runs once per artist — MetadataFetchedAt is set the first time regardless
// of outcome, so an artist TheAudioDB doesn't have is never retried on
// every subsequent scan. Best-effort: one artist's failure (a dead network,
// TheAudioDB down) is logged and skipped rather than aborting the rest.
func (s *server) cacheNewArtistsMetadata(ctx context.Context) {
	artists, err := s.musicStore.ListArtists()
	if err != nil {
		slog.Warn("music scan: listing artists for metadata caching", "error", err)
		return
	}
	for _, a := range artists {
		if a.MetadataFetchedAt != nil {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.refreshMusicArtistMetadata(ctx, a.ID, a.MBID); err != nil {
			slog.Warn("music scan: caching metadata for new artist", "artist", a.Name, "error", err)
		}
	}
}

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
	found, errs, err := s.indexers.SearchAll(ctx, query, wanted.Title, "music")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	blocked, err := s.downloads.Store().BlockedKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	prefs := release.PreferencesFor(s.store, "music")
	candidates := make([]release.Candidate, 0, len(found))
	for _, rel := range found {
		if download.IsBlocked(blocked, rel.GUID, rel.Title) {
			continue
		}
		candidates = append(candidates, release.Score(rel, prefs))
	}
	release.Rank(candidates)

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

	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	result, _, err := s.downloads.GrabRelease(ctx, req.Protocol, req.DownloadURL, req.Title, req.GUID, wanted.ID, "music")
	if errors.Is(err, download.ErrNoClient) {
		writeError(w, http.StatusServiceUnavailable,
			"no enabled "+req.Protocol+" download client — add one under Settings")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	album, err := s.musicStore.GetAlbum(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	artist, err := s.musicStore.GetArtist(album.ArtistID)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}

	profile, err := s.store.DefaultProfile("music")
	if err != nil {
		writeError(w, http.StatusBadRequest, "no default music quality profile configured")
		return
	}
	if !profile.UpgradesAllowed {
		writeError(w, http.StatusBadRequest,
			`upgrades are not enabled on the music quality profile — turn on "Allow upgrades" under Settings → Quality Profiles first`)
		return
	}

	prefs := release.PreferencesFor(s.store, "music")
	files, err := s.musicStore.ListTrackFilesByAlbum(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	currentBest := 0
	for _, f := range files {
		if sc, ok := prefs.FormatScores[strings.ToLower(f.Format)]; ok && sc > currentBest {
			currentBest = sc
		}
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
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()
	query := artist.Name + " " + album.Title
	found, errs, err := s.indexers.SearchAll(ctx, query, album.Title, "music")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	blocked, err := s.downloads.Store().BlockedKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	upgradePrefs := prefs
	upgradePrefs.MinFormatScore = currentBest
	candidates := make([]release.Candidate, 0, len(found))
	for _, rel := range found {
		if download.IsBlocked(blocked, rel.GUID, rel.Title) {
			continue
		}
		candidates = append(candidates, release.Score(rel, upgradePrefs))
	}
	release.Rank(candidates)

	writeJSON(w, http.StatusOK, map[string]any{"releases": candidates, "errors": errs})
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
	if _, err := s.musicStore.GetAlbum(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	result, _, err := s.downloads.GrabRelease(ctx, req.Protocol, req.DownloadURL, req.Title, req.GUID, 0, "music")
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
