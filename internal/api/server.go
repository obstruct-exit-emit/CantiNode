// Package api implements CantiNode's own versioned REST API (/api/v1) —
// root folders, library browsing, unmatched-file review, scan control,
// organize preview/apply, and settings — the same API the embedded web UI
// (web/) is built on.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cantinode/cantinode/internal/acquisition"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/coverart"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/scanner"
)

// Server is CantiNode's native API.
type Server struct {
	version     string
	db          *database.DB
	scanner     *scanner.Scanner
	coverart    *coverart.Client
	acquisition *acquisition.Service
	mux         *http.ServeMux

	cfgMu      sync.Mutex
	cfg        *config.Config
	configPath string

	scanMu    sync.Mutex
	scanState scanState
}

// scanState is the last (or currently running) scan's status, reported by
// GET /api/v1/scan/status and updated by the goroutine POST
// /api/v1/scan starts.
type scanState struct {
	Running    bool                `json:"running"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	Result     *scanner.ScanResult `json:"result,omitempty"`
	Error      string              `json:"error,omitempty"`
}

// NewServer builds the native API server. version is a free-form build
// identifier. cfg is the live, in-memory configuration; changes made
// through PUT /api/v1/settings are applied to it and persisted to
// configPath immediately.
func NewServer(version string, db *database.DB, sc *scanner.Scanner, ca *coverart.Client, aq *acquisition.Service, cfg *config.Config, configPath string) *Server {
	s := &Server{
		version:     version,
		db:          db,
		scanner:     sc,
		coverart:    ca,
		acquisition: aq,
		cfg:         cfg,
		configPath:  configPath,
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/version", s.requireAuth(s.handleVersion))

	s.mux.HandleFunc("GET /api/v1/root-folders", s.requireAuth(s.handleListRootFolders))
	s.mux.HandleFunc("POST /api/v1/root-folders", s.requireAuth(s.handleCreateRootFolder))
	s.mux.HandleFunc("DELETE /api/v1/root-folders/{id}", s.requireAuth(s.handleDeleteRootFolder))
	s.mux.HandleFunc("GET /api/v1/browse-directories", s.requireAuth(s.handleBrowseDirectories))

	s.mux.HandleFunc("GET /api/v1/artists", s.requireAuth(s.handleListArtists))
	s.mux.HandleFunc("GET /api/v1/artists/{id}", s.requireAuth(s.handleGetArtist))
	s.mux.HandleFunc("GET /api/v1/artists/{id}/albums", s.requireAuth(s.handleListAlbumsByArtist))
	s.mux.HandleFunc("GET /api/v1/albums/{id}/tracks", s.requireAuth(s.handleListTracksByAlbum))
	// Not requireAuth: an <img src> tag can't send an Authorization
	// header, so this route accepts the key via ?api_key= too — see
	// requireAuthHeaderOrQuery's own doc comment.
	s.mux.HandleFunc("GET /api/v1/albums/{id}/cover", s.requireAuthHeaderOrQuery(s.handleAlbumCover))
	s.mux.HandleFunc("GET /api/v1/tracks/{id}/files", s.requireAuth(s.handleListTrackFilesByTrack))

	s.mux.HandleFunc("GET /api/v1/track-files/unmatched", s.requireAuth(s.handleListUnmatched))
	s.mux.HandleFunc("POST /api/v1/track-files/{id}/match", s.requireAuth(s.handleManualMatch))
	s.mux.HandleFunc("DELETE /api/v1/track-files/{id}/match", s.requireAuth(s.handleClearMatch))
	s.mux.HandleFunc("GET /api/v1/track-files/{id}/organize/preview", s.requireAuth(s.handlePreviewOrganize))
	s.mux.HandleFunc("POST /api/v1/track-files/{id}/organize", s.requireAuth(s.handleOrganize))
	s.mux.HandleFunc("POST /api/v1/track-files/{id}/write-tags", s.requireAuth(s.handleWriteTags))
	s.mux.HandleFunc("DELETE /api/v1/track-files/{id}", s.requireAuth(s.handleDeleteTrackFile))

	s.mux.HandleFunc("GET /api/v1/musicbrainz/search", s.requireAuth(s.handleMusicBrainzSearch))

	s.mux.HandleFunc("POST /api/v1/scan", s.requireAuth(s.handleTriggerScan))
	s.mux.HandleFunc("GET /api/v1/scan/status", s.requireAuth(s.handleScanStatus))

	s.mux.HandleFunc("GET /api/v1/settings", s.requireAuth(s.handleGetSettings))
	s.mux.HandleFunc("PUT /api/v1/settings", s.requireAuth(s.handleUpdateSettings))

	// Acquisition (optional — see internal/acquisition's own doc comment):
	// monitor artists, want their albums, search Prowlarr, grab via
	// qBittorrent or SABnzbd. Artist-scoped rather than the old separate
	// "monitored artists" resource — see migrations/0004_unified_artist.sql
	// and internal/database/artists.go's doc comment.
	s.mux.HandleFunc("GET /api/v1/musicbrainz/artist-search", s.requireAuth(s.handleArtistSearch))
	s.mux.HandleFunc("POST /api/v1/artists/monitor", s.requireAuth(s.handleMonitorArtistByMBID))
	s.mux.HandleFunc("POST /api/v1/artists/{id}/monitor", s.requireAuth(s.handleMonitorArtistByID))
	s.mux.HandleFunc("POST /api/v1/artists/{id}/unmonitor", s.requireAuth(s.handleUnmonitorArtist))
	s.mux.HandleFunc("POST /api/v1/artists/{id}/refresh-metadata", s.requireAuth(s.handleRefreshArtistMetadata))
	s.mux.HandleFunc("GET /api/v1/artists/{id}/missing", s.requireAuth(s.handleListMissingReleaseGroups))
	s.mux.HandleFunc("POST /api/v1/artists/{id}/wanted", s.requireAuth(s.handleWantArtistAlbum))
	s.mux.HandleFunc("GET /api/v1/artists/{id}/wanted", s.requireAuth(s.handleListWantedAlbums))
	s.mux.HandleFunc("GET /api/v1/artists/{id}/organize/preview", s.requireAuth(s.handlePreviewOrganizeArtist))
	s.mux.HandleFunc("POST /api/v1/artists/{id}/organize", s.requireAuth(s.handleOrganizeArtist))
	s.mux.HandleFunc("DELETE /api/v1/artists/{id}", s.requireAuth(s.handleRemoveArtist))
	s.mux.HandleFunc("POST /api/v1/wanted-albums/{id}/ignore", s.requireAuth(s.handleIgnoreWantedAlbum))
	s.mux.HandleFunc("GET /api/v1/wanted-albums/{id}/search", s.requireAuth(s.handleSearchReleases))
	s.mux.HandleFunc("POST /api/v1/wanted-albums/{id}/grab", s.requireAuth(s.handleGrabRelease))
	s.mux.HandleFunc("GET /api/v1/downloads", s.requireAuth(s.handleListDownloads))
	s.mux.HandleFunc("DELETE /api/v1/downloads/{id}", s.requireAuth(s.handleCancelDownload))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": s.version})
}

// requireAuth checks the Authorization: Bearer <api_key> header against
// the live config (so a regenerated key takes effect immediately, no
// restart needed) — CantiNode's v1 auth model is API-key-only, matching
// LibriNode/AcerviNode's own base tier before either added optional login
// accounts on top.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.cfgMu.Lock()
		want := s.cfg.APIKey
		s.cfgMu.Unlock()
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// requireAuthHeaderOrQuery is requireAuth plus a ?api_key= query
// parameter fallback — for the one route the web UI can't attach a
// bearer token to at all: a plain HTML <img src="..."> request. Every
// other route stays header-only, matching the *arr-ecosystem convention
// of only relaxing this for actual image endpoints, not the API broadly.
func (s *Server) requireAuthHeaderOrQuery(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" {
			key = r.URL.Query().Get("api_key")
		}
		s.cfgMu.Lock()
		want := s.cfg.APIKey
		s.cfgMu.Unlock()
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	http.Error(w, err.Error(), status)
}

// pathID parses the {id} path value from r as an int64, or writes a 400
// and returns ok=false.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return 0, false
	}
	return id, true
}

// scanContext is used instead of a request's own context for the
// goroutine POST /api/v1/scan starts — the request's context is canceled
// the moment the handler returns, which is before a real scan (rate
// limited to ~1 MusicBrainz call/sec) could ever finish.
func scanContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
