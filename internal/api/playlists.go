// Playlists: user-curated ordered lists of tracks, independent of any
// album/artist. CantiNode has no player of its own — export as a standard
// M3U is how a playlist actually gets used, in whatever real player points
// at the same library (Navidrome, Plex, Kodi, VLC, ...).
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

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/plex"
)

func (s *server) handleListPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := s.musicStore.ListPlaylists()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playlists)
}

func (s *server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := s.musicStore.CreatePlaylist(req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.syncPlaylistsInBackground()
	writeJSON(w, http.StatusCreated, p)
}

// syncPlaylistsInBackground kicks off a Plex playlist sync pass right
// after a playlist changes, instead of leaving it to the next periodic
// poll (up to PollInterval away) — same "don't make the user wait for the
// next scheduled pass to see their own edit" reasoning as refreshHealth's
// own post-change re-check. Non-blocking: a sync pass costs at least one
// full-library track listing plus a request per playlist that actually
// changed, too slow to hold the HTTP response open for. Safe to call
// unconditionally from every mutating handler — skips outright (no
// goroutine spun up at all) when playlist sync isn't configured or the
// operator has opted out of the immediate pass via
// PlaylistSyncOnChangeDisabled, in which case the change still reaches
// Plex on the next periodic pass regardless.
func (s *server) syncPlaylistsInBackground() {
	settings := s.cfg.PlexSettings()
	if !settings.PlaylistSyncReady() || settings.PlaylistSyncOnChangeDisabled {
		return
	}
	go s.plexPlaylistSync.PollOnce(context.Background())
}

// handleSyncPlaylists runs a playlist sync pass right now instead of
// waiting for the next periodic one — Playlists' own "Sync now" button.
// Unlike syncPlaylistsInBackground's fire-and-forget use elsewhere, this
// is the one place a human is actually waiting on the result, so it runs
// synchronously and reports back what happened.
func (s *server) handleSyncPlaylists(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.PlexSettings().PlaylistSyncReady() {
		writeError(w, http.StatusBadRequest, "playlist sync isn't configured — enable it under Settings → Integrations first")
		return
	}
	writeJSON(w, http.StatusOK, s.plexPlaylistSync.PollOnce(r.Context()))
}

// playlistDetail is a playlist plus its ordered tracks — the single round
// trip the detail page needs.
type playlistDetail struct {
	musiclibrary.Playlist
	Tracks []musiclibrary.PlaylistTrack `json:"tracks"`
}

func (s *server) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := s.musicStore.GetPlaylist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	tracks, err := s.musicStore.ListPlaylistTracks(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playlistDetail{Playlist: *p, Tracks: tracks})
}

func (s *server) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.musicStore.UpdatePlaylist(id, req.Name, req.Description); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	p, err := s.musicStore.GetPlaylist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	s.syncPlaylistsInBackground()
	writeJSON(w, http.StatusOK, p)
}

func (s *server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Read the playlist before deleting it so its own Plex link (if any)
	// is still known afterward — DeletePlaylist itself only reports
	// row-affected, not what the row used to contain.
	p, err := s.musicStore.GetPlaylist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	if err := s.musicStore.DeletePlaylist(id); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	if p.PlexRatingKey != "" {
		s.propagatePlaylistDelete(p.PlexRatingKey)
	}
	w.WriteHeader(http.StatusNoContent)
}

// propagatePlaylistDelete reacts to a linked playlist just deleted on
// CantiNode's own side. Handled here, synchronously (not left to the next
// internal/plexplaylistsync poll), since waiting up to PollInterval to
// reflect something CantiNode itself already knows happened would be a
// needless delay for a user-initiated action. Records a tombstone
// unconditionally — see migration 036's own comment — so a later sync pass
// never "resurrects" this playlist as new just because Plex's own copy
// still exists (whether it was left in place by "unlink" mode, or a
// "propagate" delete below transiently failed to reach Plex).
func (s *server) propagatePlaylistDelete(ratingKey string) {
	if err := s.musicStore.RecordPlexPlaylistTombstone(ratingKey); err != nil {
		slog.Default().Warn("plex: recording playlist tombstone", "ratingKey", ratingKey, "error", err)
	}
	settings := s.cfg.PlexSettings()
	if !settings.PlaylistDeletePropagates() || settings.ServerURL == "" || settings.Token == "" {
		return
	}
	go func() {
		client := plex.NewClient(settings.ServerURL, settings.Token)
		if err := client.DeletePlaylist(context.Background(), ratingKey); err != nil {
			slog.Default().Warn("plex: deleting linked playlist", "ratingKey", ratingKey, "error", err)
		}
	}()
}

// writeAppendPlaylistError maps AppendPlaylistItem(s)'s two distinct
// not-found cases correctly: a missing playlist is the URL's own resource
// (404, via writeMusicStoreError's usual ErrNotFound handling), but a bad
// track id is bad request *content* against a playlist that's perfectly
// real (400) — conflating the two would either 404 a request whose URL is
// fine, or (before this existed) let a track id fall all the way through
// to an unhandled SQLite foreign-key-constraint 500.
func writeAppendPlaylistError(w http.ResponseWriter, err error) {
	if errors.Is(err, musiclibrary.ErrTrackNotFound) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeMusicStoreError(w, err)
}

func (s *server) handleAppendPlaylistItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		TrackID int64 `json:"trackId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TrackID <= 0 {
		writeError(w, http.StatusBadRequest, "trackId is required")
		return
	}
	item, err := s.musicStore.AppendPlaylistItem(id, req.TrackID)
	if err != nil {
		writeAppendPlaylistError(w, err)
		return
	}
	s.syncPlaylistsInBackground()
	writeJSON(w, http.StatusCreated, item)
}

// handleAppendPlaylistItemsBulk adds several tracks in one request — an
// album's whole tracklist added in one call, rather than one round trip
// per track (and one transaction, so a failure partway through never
// leaves half an album added).
func (s *server) handleAppendPlaylistItemsBulk(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		TrackIDs []int64 `json:"trackIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.TrackIDs) == 0 {
		writeError(w, http.StatusBadRequest, "trackIds is required")
		return
	}
	items, err := s.musicStore.AppendPlaylistItems(id, req.TrackIDs)
	if err != nil {
		writeAppendPlaylistError(w, err)
		return
	}
	s.syncPlaylistsInBackground()
	writeJSON(w, http.StatusCreated, items)
}

// handleSearchOwnedTracks answers the Search page's track results — only
// owned tracks a playlist could actually use (see SearchOwnedTracks' own
// doc comment on why a file-less track is excluded).
func (s *server) handleSearchOwnedTracks(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []musiclibrary.TrackSearchResult{})
		return
	}
	results, err := s.musicStore.SearchOwnedTracks(q, 24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// handleListPlaylistsForTrack backs the album track list's "in playlist"
// badge — which playlist(s) is this track actually in, and a way to jump
// to one.
func (s *server) handleListPlaylistsForTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	playlists, err := s.musicStore.ListPlaylistsForTrack(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playlists)
}

func (s *server) handleRemovePlaylistItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	itemID, err := strconv.ParseInt(r.PathValue("itemId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	if err := s.musicStore.RemovePlaylistItem(id, itemID); err != nil {
		writeMusicStoreError(w, err)
		return
	}
	s.syncPlaylistsInBackground()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleReorderPlaylistItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ItemIDs []int64 `json:"itemIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.musicStore.ReorderPlaylistItems(id, req.ItemIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tracks, err := s.musicStore.ListPlaylistTracks(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.syncPlaylistsInBackground()
	writeJSON(w, http.StatusOK, tracks)
}

// playlistFilenameUnsafe matches characters no real filesystem accepts —
// the same class direct.go's safeFilename guards against, for the same
// reason: a playlist named with one shouldn't break the exported file.
var playlistFilenameUnsafe = regexp.MustCompile(`[/\\:*?"<>|]`)

// handleExportPlaylist writes playlistID out as a standard extended M3U —
// the only "player" CantiNode itself offers: a file any real one
// (Navidrome, Plex, Kodi, VLC, ...) pointed at the same library can load.
// A track with nothing currently backing it (deleted, never matched) is
// skipped rather than writing a path that doesn't exist.
func (s *server) handleExportPlaylist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := s.musicStore.GetPlaylist(id)
	if err != nil {
		writeMusicStoreError(w, err)
		return
	}
	tracks, err := s.musicStore.ListPlaylistTracks(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, t := range tracks {
		if t.Path == "" {
			continue
		}
		artist := t.ArtistName
		fmt.Fprintf(&b, "#EXTINF:%d,%s - %s\n", t.DurationMs/1000, artist, t.Title)
		b.WriteString(t.Path)
		b.WriteString("\n")
	}

	name := playlistFilenameUnsafe.ReplaceAllString(p.Name, "_")
	if name == "" {
		name = "playlist"
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.m3u"`)
	w.Write([]byte(b.String()))
}
