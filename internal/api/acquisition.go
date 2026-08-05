package api

import (
	"encoding/json"
	"fmt"
	"net/http"

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

func (s *Server) handleListMonitoredArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := s.db.ListMonitoredArtists(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, artists)
}

type monitorArtistRequest struct {
	MBID string `json:"mbid"`
}

func (s *Server) handleMonitorArtist(w http.ResponseWriter, r *http.Request) {
	var req monitorArtistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.MBID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("mbid must not be empty"))
		return
	}
	m, err := s.acquisition.MonitorArtist(r.Context(), req.MBID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, m)
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

func (s *Server) handleSyncArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.acquisition.SyncArtist(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
