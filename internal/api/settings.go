package api

import (
	"encoding/json"
	"net/http"

	"github.com/cantinode/cantinode/internal/acervinode"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/prowlarr"
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

	// Prowlarr/AcerviNode connection details for the optional acquisition
	// pipeline (see internal/acquisition) — both blank means "not
	// configured," not an error.
	ProwlarrURL      string `json:"prowlarr_url"`
	ProwlarrAPIKey   string `json:"prowlarr_api_key"`
	AcerviNodeURL    string `json:"acervinode_url"`
	AcerviNodeAPIKey string `json:"acervinode_api_key"`
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
		ProwlarrURL:             cfg.ProwlarrURL,
		ProwlarrAPIKey:          cfg.ProwlarrAPIKey,
		AcerviNodeURL:           cfg.AcerviNodeURL,
		AcerviNodeAPIKey:        cfg.AcerviNodeAPIKey,
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
	candidate.ProwlarrURL = req.ProwlarrURL
	candidate.ProwlarrAPIKey = req.ProwlarrAPIKey
	candidate.AcerviNodeURL = req.AcerviNodeURL
	candidate.AcerviNodeAPIKey = req.AcerviNodeAPIKey

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
	s.acquisition.UpdateClients(buildProwlarrClient(candidate, s.version), buildAcerviClient(candidate))

	writeJSON(w, settingsFromConfig(s.cfg))
}

// buildProwlarrClient/buildAcerviClient return nil (meaning "not
// configured" — see internal/acquisition) when their respective URL is
// blank, rather than a Client that would just fail every call against an
// empty base URL.

func buildProwlarrClient(cfg config.Config, version string) *prowlarr.Client {
	if cfg.ProwlarrURL == "" {
		return nil
	}
	return prowlarr.NewClient(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, "CantiNode/"+version+" ( https://github.com/cantinode/cantinode )")
}

func buildAcerviClient(cfg config.Config) *acervinode.Client {
	if cfg.AcerviNodeURL == "" {
		return nil
	}
	return acervinode.NewClient(cfg.AcerviNodeURL, cfg.AcerviNodeAPIKey)
}
