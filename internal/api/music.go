// Music: artists/albums/tracks browsing, monitoring, scanning, manual
// matching, and organizing — ported from CantiNode's own original
// (pre-LibriNode-fork) API, adapted to musiclibrary/musicscanner and to
// LibriNode's existing indexer/download-client pipeline instead of the old
// standalone Prowlarr/qBittorrent/SABnzbd clients.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/coverart"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/musiclibrary"
	"github.com/librinode/librinode/internal/musicscanner"
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

	meta, err := s.audiodb.LookupArtistByMBID(ctx, mbid)
	if err != nil || meta == nil {
		return nil // best-effort; a missing bio/photo is cosmetic, not fatal
	}
	return s.musicStore.SetArtistMetadata(artistID, meta.Bio, meta.ImageURL, now)
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

// handleRemoveMusicArtist detaches (unlinks, per DeleteArtist's own FK
// warning) every track file the artist owns before deleting the row —
// optionally also deleting those files from disk.
func (s *server) handleRemoveMusicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"

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

func (s *server) handleIgnoreWantedMusicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicStore.SetWantedAlbumStatus(id, musiclibrary.WantedStatusIgnored); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSearchWantedMusicAlbum searches every enabled indexer for a wanted
// album — the query is the artist's name plus the album's own title, which
// in practice finds the right release across arbitrary indexer naming
// conventions far more reliably than either alone.
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
	writeJSON(w, http.StatusOK, map[string]any{"releases": found, "errors": errs})
}

// handleGrabWantedMusicAlbum sends a release (a result from a prior
// handleSearchWantedMusicAlbum call — the caller passes back whichever one
// the user picked) to the matching download client. Grabs are recorded
// untracked (no book_id — that column belongs to the prose/comic library):
// internal/musicscanner's own scan matches the downloaded files by their
// embedded MusicBrainz tags once they land in a music root folder, the
// same as any other file dropped there, so a grab doesn't need Completed
// Download Handling's book-tracking machinery to eventually show up.
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
	if err := s.musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
