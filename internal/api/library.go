package api

import (
	"errors"
	"net/http"

	"github.com/cantinode/cantinode/internal/database"
)

func (s *Server) handleListArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := s.db.ListArtists(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, artists)
}

func (s *Server) handleListAlbumsByArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	albums, err := s.db.ListAlbumsByArtist(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, albums)
}

func (s *Server) handleListTracksByAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	tracks, err := s.db.ListTracksByAlbum(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, tracks)
}

func (s *Server) handleListTrackFilesByTrack(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	files, err := s.db.ListTrackFilesByTrack(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, files)
}

func (s *Server) handleListUnmatched(w http.ResponseWriter, r *http.Request) {
	files, err := s.db.ListTrackFilesByStatus(r.Context(), database.StatusUnmatched)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, files)
}

// notFoundStatus maps database.ErrNotFound to 404, anything else to 500 —
// shared by every handler that looks a single row up by ID.
func notFoundStatus(err error) int {
	if errors.Is(err, database.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
