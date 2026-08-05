package api

import (
	"errors"
	"net/http"

	"github.com/cantinode/cantinode/internal/coverart"
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

// handleAlbumCover serves an album's cached front cover image, fetching
// and caching it from Cover Art Archive on first request — see
// internal/coverart. 404s (not an error body — this is an <img> src)
// both when the album itself doesn't exist and when it has no cover art,
// so a broken-image icon is the only user-visible difference; the web UI
// doesn't currently distinguish the two.
func (s *Server) handleAlbumCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	album, err := s.db.GetAlbum(r.Context(), id)
	if err != nil {
		w.WriteHeader(notFoundStatus(err))
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
		writeError(w, http.StatusBadGateway, err)
		return
	}

	// Cover Art Archive content never changes for a given release MBID
	// once cached (a new upload there gets a new MBID association, not
	// an in-place replacement) — safe for the browser to cache
	// indefinitely rather than re-requesting on every Library visit.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType)
	http.ServeFile(w, r, path)
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
