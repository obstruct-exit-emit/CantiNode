package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
	"github.com/cantinode/cantinode/internal/tagwriter"
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
	// musicScanner keeps its own in-memory copy of the naming template (see
	// Scanner.UpdateSettings's own doc comment: "takes effect on the very
	// next file scanned/organized, no restart needed") — found live: a
	// template change saved here alone never reached it, since only
	// handlePutMusicSettings called UpdateSettings. Organize kept planning
	// paths against the stale template until the next process restart, so
	// a template edit that genuinely changed a file's target path still
	// reported "already organized" (the plan was computed from the old
	// template, which the file already matched). Pass through the current
	// Music settings' own two fields unchanged — this handler has no
	// reason to touch either of them.
	// Re-read rather than reusing req.MusicFile directly: an empty
	// submitted field means "reset to default" and SetNaming resolves that
	// against its own copy, not this handler's local req.
	ns := s.cfg.NamingSettings()
	m := s.cfg.MusicSettings()
	s.musicScanner.UpdateSettings(ns.MusicFile, m.MinMatchConfidence, m.OrganizeOnMatch, tagWriteToggles(s.cfg.TagWriteSettings()))
	writeJSON(w, http.StatusOK, namingResponse(ns))
}

// --- Tag-write field toggles ---

// tagWriteToggles converts config.TagWriteSettings' negative-polarity
// "Disable*" flags (see that type's own doc comment for why they're
// inverted) into tagwriter.Toggles' positive-polarity "will this field get
// written" flags Scanner/tagwriter actually consult — the one place that
// inversion happens, so every other caller on either side of it just deals
// with the polarity native to its own layer.
func tagWriteToggles(t config.TagWriteSettings) tagwriter.Toggles {
	return tagwriter.Toggles{
		Title:                     !t.DisableTitle,
		Artist:                    !t.DisableArtist,
		AlbumArtist:               !t.DisableAlbumArtist,
		Album:                     !t.DisableAlbum,
		TrackNumber:               !t.DisableTrackNumber,
		DiscNumber:                !t.DisableDiscNumber,
		Date:                      !t.DisableDate,
		TrackTotal:                !t.DisableTrackTotal,
		DiscTotal:                 !t.DisableDiscTotal,
		Genre:                     !t.DisableGenre,
		ReleaseType:               !t.DisableReleaseType,
		ArtistSortName:            !t.DisableArtistSortName,
		AlbumArtistSortName:       !t.DisableAlbumArtistSortName,
		ReleaseCountry:            !t.DisableReleaseCountry,
		ReleaseStatus:             !t.DisableReleaseStatus,
		Media:                     !t.DisableMedia,
		Mood:                      !t.DisableMood,
		Composer:                  !t.DisableComposer,
		CoverImage:                !t.DisableCoverImage,
		MusicBrainzArtistID:       !t.DisableMusicBrainzArtistID,
		AlbumArtistID:             !t.DisableAlbumArtistID,
		MusicBrainzAlbumID:        !t.DisableMusicBrainzAlbumID,
		MusicBrainzReleaseGroupID: !t.DisableMusicBrainzReleaseGroupID,
		MusicBrainzRecordingID:    !t.DisableMusicBrainzRecordingID,
	}
}

func (s *server) handleGetTagWriteSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.TagWriteSettings())
}

func (s *server) handlePutTagWriteSettings(w http.ResponseWriter, r *http.Request) {
	var req config.TagWriteSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetTagWrite(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	ns := s.cfg.NamingSettings()
	m := s.cfg.MusicSettings()
	s.musicScanner.UpdateSettings(ns.MusicFile, m.MinMatchConfidence, m.OrganizeOnMatch, tagWriteToggles(req))
	writeJSON(w, http.StatusOK, req)
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
