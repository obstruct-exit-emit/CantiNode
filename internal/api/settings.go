package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
)

// --- Naming settings ---

// exampleMusicArtist/Album/Track render a music naming template preview
// with a recognizable album.
var (
	exampleMusicArtist = musiclibrary.Artist{Name: "Boards of Canada"}
	exampleMusicAlbum  = musiclibrary.Album{Title: "Geogaddi", ReleaseDate: "2002-02-04"}
	exampleMusicTrack  = musiclibrary.Track{Title: "Alpha and Omega", TrackNumber: 3, DiscNumber: 1}
)

type namingSettingsResponse struct {
	config.NamingSettings
	MusicExample string `json:"musicExample"`
}

func namingResponse(ns config.NamingSettings) namingSettingsResponse {
	return namingSettingsResponse{
		NamingSettings: ns,
		MusicExample: filepath.ToSlash(musicscanner.FormatPath(
			ns.MusicFile, exampleMusicArtist, exampleMusicAlbum, exampleMusicTrack, ".mp3",
		)),
	}
}

func (s *server) handleGetNamingSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, namingResponse(s.cfg.NamingSettings()))
}

func (s *server) handlePutNamingSettings(w http.ResponseWriter, r *http.Request) {
	var req config.NamingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Empty fields fall back to defaults (SetNaming fills them), so a
	// partial payload can never wipe the music template.
	if err := s.cfg.SetNaming(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, namingResponse(s.cfg.NamingSettings()))
}

// --- Remote path mappings ---

func (s *server) handleGetPathMappings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.PathMappings())
}

// handlePutPathMappings replaces the whole mapping list (the UI edits it as
// one small table).
func (s *server) handlePutPathMappings(w http.ResponseWriter, r *http.Request) {
	var req []config.PathMapping
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetPathMappings(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.PathMappings())
}

// --- Background timing settings ---

func (s *server) handleGetTimingSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.TimingSettings())
}

// handlePutTimingSettings saves the background-loop cadences. Values are
// clamped by SetTimings; changes take effect on the next server start.
func (s *server) handlePutTimingSettings(w http.ResponseWriter, r *http.Request) {
	var req config.TimingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetTimings(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.TimingSettings())
}
