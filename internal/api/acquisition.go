package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/prowlarr"
)

func (s *Server) handleArtistSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	artists, err := s.acquisition.SearchArtists(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, artists)
}

type monitorArtistRequest struct {
	MBID string `json:"mbid"`
}

// handleMonitorArtistByMBID monitors an artist CantiNode may not know
// about at all yet — the "monitor an artist" search flow, which only has
// a MusicBrainz search result (an MBID) to go on, not a local artist id.
func (s *Server) handleMonitorArtistByMBID(w http.ResponseWriter, r *http.Request) {
	var req monitorArtistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.MBID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mbid must not be empty"))
		return
	}
	a, err := s.acquisition.MonitorArtist(r.Context(), req.MBID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, a)
}

// handleMonitorArtistByID starts monitoring an artist CantiNode already
// has a row for (typically one it only knows about from owned files) —
// the unified artist page's own "Monitor" button. Resolves to the same
// acquisition.MonitorArtist call as handleMonitorArtistByMBID once the
// row's own mbid is known; MonitorArtist is idempotent either way (see
// database.GetOrCreateArtist).
func (s *Server) handleMonitorArtistByID(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	existing, err := s.db.GetArtist(r.Context(), id)
	if err != nil {
		writeError(w, notFoundStatus(err), err)
		return
	}
	a, err := s.acquisition.MonitorArtist(r.Context(), existing.MBID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, a)
}

func (s *Server) handleUnmonitorArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.acquisition.UnmonitorArtist(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefreshArtistMetadata re-fetches an artist's cached discography
// and bio/image — the unified artist page's "Refresh metadata" button.
// Works whether or not the artist is currently monitored.
func (s *Server) handleRefreshArtistMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.acquisition.RefreshArtistMetadata(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// artistDetail is database.Artist plus its owned-album count — the
// unified artist page's header. The albums themselves stay on the
// existing GET /api/v1/artists/{id}/albums rather than being duplicated
// here.
type artistDetail struct {
	database.Artist
	OwnedAlbumCount int `json:"owned_album_count"`
}

func (s *Server) handleGetArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a, err := s.db.GetArtist(r.Context(), id)
	if err != nil {
		writeError(w, notFoundStatus(err), err)
		return
	}
	albums, err := s.db.ListAlbumsByArtist(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, artistDetail{Artist: *a, OwnedAlbumCount: len(albums)})
}

// handleListMissingReleaseGroups backs the unified artist page's
// "Missing" section — cached discography (internal/audiodb's
// counterpart on the MusicBrainz side, artist_release_groups) minus
// whatever's already owned or already wanted. Returned as a flat list;
// grouping by release type is left to the frontend, which already needs
// to bucket Album/EP/Live/Compilation/Other for display regardless of
// how the API shapes the response.
func (s *Server) handleListMissingReleaseGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	groups, err := s.db.ListMissingArtistReleaseGroups(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, groups)
}

type wantArtistAlbumRequest struct {
	ReleaseGroupMBID string `json:"release_group_mbid"`
	Monitor          bool   `json:"monitor"`
}

// handleWantArtistAlbum is the unified artist page's per-row/bulk
// "Add"/"Add & Monitor" action. Monitor=true additionally flips the
// artist's own IsMonitored flag on — still no auto-grab either way (see
// ROADMAP.md's v1 scoping), just marks the artist as actively tracked.
func (s *Server) handleWantArtistAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req wantArtistAlbumRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.ReleaseGroupMBID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("release_group_mbid must not be empty"))
		return
	}

	wanted, err := s.acquisition.AddWantedAlbum(r.Context(), id, req.ReleaseGroupMBID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Monitor {
		if err := s.db.SetArtistMonitored(r.Context(), id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, wanted)
}

func (s *Server) handleListWantedAlbums(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	wanted, err := s.db.ListWantedAlbumsByArtist(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, wanted)
}

func (s *Server) handleIgnoreWantedAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.acquisition.IgnoreWantedAlbum(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSearchReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	releases, err := s.acquisition.SearchReleases(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, releases)
}

func (s *Server) handleGrabRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var rel prowlarr.Release
	if err := json.NewDecoder(r.Body).Decode(&rel); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	download, err := s.acquisition.GrabRelease(r.Context(), id, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, download)
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	downloads, err := s.db.ListDownloads(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, downloads)
}

// handleCancelDownload cancels a grab — best-effort removes it from its
// download client and reverts the wanted album back to "wanted" — see
// acquisition.Service.CancelDownload.
func (s *Server) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.acquisition.CancelDownload(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
