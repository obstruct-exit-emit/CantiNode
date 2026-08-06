package api

import (
	"encoding/json"
	"net/http"

	"github.com/cantinode/cantinode/internal/audiodb"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/qbittorrent"
	"github.com/cantinode/cantinode/internal/sabnzbd"
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

	// Prowlarr/qBittorrent/SABnzbd connection details for the optional
	// acquisition pipeline (see internal/acquisition) — a blank URL means
	// "not configured," not an error. qBittorrent and SABnzbd are each
	// independent: point one, both, or neither at AcerviNode's own compat
	// shims, or at genuine standalone instances.
	ProwlarrURL         string `json:"prowlarr_url"`
	ProwlarrAPIKey      string `json:"prowlarr_api_key"`
	QBittorrentURL      string `json:"qbittorrent_url"`
	QBittorrentUsername string `json:"qbittorrent_username"`
	QBittorrentPassword string `json:"qbittorrent_password"`
	SABnzbdURL          string `json:"sabnzbd_url"`
	SABnzbdAPIKey       string `json:"sabnzbd_api_key"`

	// AudioDBAPIKey configures internal/audiodb's artist bio/image
	// lookup — a blank value means "use TheAudioDB's own public shared
	// key," not "not configured" (see config.Config.AudioDBAPIKey).
	AudioDBAPIKey string `json:"audiodb_api_key"`
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
		QBittorrentURL:          cfg.QBittorrentURL,
		QBittorrentUsername:     cfg.QBittorrentUsername,
		QBittorrentPassword:     cfg.QBittorrentPassword,
		SABnzbdURL:              cfg.SABnzbdURL,
		SABnzbdAPIKey:           cfg.SABnzbdAPIKey,
		AudioDBAPIKey:           cfg.AudioDBAPIKey,
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
	candidate.QBittorrentURL = req.QBittorrentURL
	candidate.QBittorrentUsername = req.QBittorrentUsername
	candidate.QBittorrentPassword = req.QBittorrentPassword
	candidate.SABnzbdURL = req.SABnzbdURL
	candidate.SABnzbdAPIKey = req.SABnzbdAPIKey
	candidate.AudioDBAPIKey = req.AudioDBAPIKey

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
	s.acquisition.UpdateClients(buildProwlarrClient(candidate, s.version), buildQBittorrentClient(candidate), buildSABnzbdClient(candidate))
	s.acquisition.UpdateAudioDBClient(audiodb.NewClient(candidate.AudioDBAPIKey))

	writeJSON(w, settingsFromConfig(s.cfg))
}

// buildProwlarrClient/buildQBittorrentClient/buildSABnzbdClient return
// nil (meaning "not configured" — see internal/acquisition) when their
// respective URL is blank, rather than a Client that would just fail
// every call against an empty base URL.

func buildProwlarrClient(cfg config.Config, version string) *prowlarr.Client {
	if cfg.ProwlarrURL == "" {
		return nil
	}
	return prowlarr.NewClient(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, "CantiNode/"+version+" ( https://github.com/cantinode/cantinode )")
}

func buildQBittorrentClient(cfg config.Config) *qbittorrent.Client {
	if cfg.QBittorrentURL == "" {
		return nil
	}
	return qbittorrent.NewClient(cfg.QBittorrentURL, cfg.QBittorrentUsername, cfg.QBittorrentPassword)
}

func buildSABnzbdClient(cfg config.Config) *sabnzbd.Client {
	if cfg.SABnzbdURL == "" {
		return nil
	}
	return sabnzbd.NewClient(cfg.SABnzbdURL, cfg.SABnzbdAPIKey)
}
