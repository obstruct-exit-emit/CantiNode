package api

import (
	"encoding/json"
	"net/http"

	"github.com/cantinode/cantinode/internal/config"
)

// settingsView is config.Config as reported to (and accepted from) the
// settings UI. Excludes DataDir (an advanced, rarely-touched path
// setting not worth a quick-edit form field, same treatment AcerviNode
// gives it) — Port and DataDir both need a restart to actually take
// effect (a live port rebind / database reopen is out of scope for v1),
// so DataDir is left config-file-only and Port is included mainly for
// visibility, not because changing it here does anything until restart.
type settingsView struct {
	APIKey                  string  `json:"api_key"`
	Port                    int     `json:"port"`
	LogLevel                string  `json:"log_level"`
	ScanIntervalHours       int     `json:"scan_interval_hours"`
	NamingFormat            string  `json:"naming_format"`
	OrganizeOnMatch         bool    `json:"organize_on_match"`
	MinMatchConfidence      float64 `json:"min_match_confidence"`
	MusicBrainzContactEmail string  `json:"musicbrainz_contact_email"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.Lock()
	view := settingsFromConfig(s.cfg)
	s.cfgMu.Unlock()
	writeJSON(w, view)
}

func settingsFromConfig(cfg *config.Config) settingsView {
	return settingsView{
		APIKey:                  cfg.APIKey,
		Port:                    cfg.Port,
		LogLevel:                cfg.LogLevel,
		ScanIntervalHours:       cfg.ScanIntervalHours,
		NamingFormat:            cfg.NamingFormat,
		OrganizeOnMatch:         cfg.OrganizeOnMatch,
		MinMatchConfidence:      cfg.MinMatchConfidence,
		MusicBrainzContactEmail: cfg.MusicBrainzContactEmail,
	}
}

// handleUpdateSettings validates and applies a candidate settings change,
// persists it to config.yaml, and pushes naming_format/
// min_match_confidence/organize_on_match into the live Scanner (see
// scanner.Scanner.UpdateSettings) so they take effect on the very next
// scan — no restart needed. Port is intentionally not writable here (see
// settingsView): it's included in the response for visibility only.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsView
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	candidate := *s.cfg
	candidate.LogLevel = req.LogLevel
	candidate.ScanIntervalHours = req.ScanIntervalHours
	candidate.NamingFormat = req.NamingFormat
	candidate.OrganizeOnMatch = req.OrganizeOnMatch
	candidate.MinMatchConfidence = req.MinMatchConfidence
	candidate.MusicBrainzContactEmail = req.MusicBrainzContactEmail

	if err := candidate.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := candidate.Save(s.configPath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	*s.cfg = candidate
	s.scanner.UpdateSettings(candidate.NamingFormat, candidate.MinMatchConfidence, candidate.OrganizeOnMatch)

	writeJSON(w, settingsFromConfig(s.cfg))
}
