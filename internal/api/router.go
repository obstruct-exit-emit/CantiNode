// Package api exposes LibriNode's versioned REST API and serves the web UI.
// Every endpoint under /api/v1 requires the API key via the X-Api-Key header
// (or ?apikey= query parameter); /ping is open for health checks.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/librinode/librinode/internal/audiodb"
	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/coverart"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/health"
	"github.com/librinode/librinode/internal/imagecache"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/musicbrainz"
	"github.com/librinode/librinode/internal/musiclibrary"
	"github.com/librinode/librinode/internal/musicscanner"
	"github.com/librinode/librinode/web"
)

type server struct {
	cfg       *config.Config
	db        *sql.DB
	store     *library.Store // root_folders + quality_profiles — generic, shared by music
	indexers  *indexer.Service
	downloads *download.Service
	health    *health.Service
	images    *imagecache.Cache
	sessions  *sessionStore
	webFS     fs.FS // nil when no frontend build is embedded
	version   string

	// Music: the whole domain model (musiclibrary) and scan/match pipeline
	// (musicscanner) — see internal/musiclibrary's own package doc comment.
	musicStore   *musiclibrary.Store
	musicScanner *musicscanner.Scanner
	mb           *musicbrainz.Client
	audiodb      *audiodb.Client
	coverart     *coverart.Client

	musicScanMu    sync.Mutex
	musicScanState musicScanState
}

// Background bundles the services main runs on periodic loops.
type Background struct {
	Health *health.Service
}

// NewRouter builds the API handler and returns the background services the
// caller runs periodically; their endpoints are already wired into the
// handler.
func NewRouter(cfg *config.Config, db *sql.DB, version string) (http.Handler, *Background) {
	store := library.NewStore(db)
	downloads := download.NewService(download.NewStore(db))
	indexers := indexer.NewService(indexer.NewStore(db))
	// Native sources that resolve their download URL lazily (a scraped
	// source's release page → magnet) do so at grab time, for the one
	// release grabbed.
	downloads.SetURLResolver(indexers.ResolveGrabURL)

	musicSettings := cfg.MusicSettings()
	musicStore := musiclibrary.NewStore(db)
	mb := musicbrainz.NewClient(version, musicSettings.MusicBrainzContactEmail)
	musicScanner := musicscanner.New(musicStore, mb, slog.Default(),
		cfg.NamingSettings().MusicFile, musicSettings.MinMatchConfidence, musicSettings.OrganizeOnMatch)

	s := &server{
		cfg:          cfg,
		db:           db,
		store:        store,
		indexers:     indexers,
		downloads:    downloads,
		health:       health.New(store, indexers, downloads),
		images:       imagecache.New(filepath.Join(cfg.DataDir(), "covers", "remote")),
		sessions:     newSessionStore(),
		version:      version,
		musicStore:   musicStore,
		musicScanner: musicScanner,
		mb:           mb,
		audiodb:      audiodb.NewClient(musicSettings.AudioDBAPIKey),
		coverart:     coverart.NewClient(filepath.Join(cfg.DataDir(), "covers", "music"), "LibriNode/"+version),
	}
	if dist, ok := web.FS(); ok {
		s.webFS = dist
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.handlePing)
	// Auth endpoints: status and login are unauthenticated by nature; the
	// rest require an existing session or the API key.
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	// First-run wizard: unauthenticated by design, but only answers/claims on
	// a fresh instance (no account, nothing configured) — see setupNeeded.
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	// Account/server-configuration surface: admin-only. handleSetUserPassword
	// is the one exception — it stays on plain auth because it self-services
	// (any signed-in account may change its own password; the handler itself
	// checks admin-or-self).
	mux.HandleFunc("PUT /api/v1/auth/credentials", s.requireAdmin(s.handleSetCredentials))
	mux.HandleFunc("GET /api/v1/auth/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("POST /api/v1/auth/users", s.requireAdmin(s.handleAddUser))
	mux.HandleFunc("DELETE /api/v1/auth/users/{username}", s.requireAdmin(s.handleRemoveUser))
	mux.HandleFunc("PUT /api/v1/auth/users/{username}/password", s.auth(s.handleSetUserPassword))
	mux.HandleFunc("PUT /api/v1/auth/users/{username}/default", s.requireAdmin(s.handleMakeDefaultUser))
	mux.HandleFunc("PUT /api/v1/auth/users/{username}/role", s.requireAdmin(s.handleSetUserRole))
	mux.HandleFunc("POST /api/v1/auth/apikey/regenerate", s.requireAdmin(s.handleRegenerateAPIKey))
	mux.HandleFunc("GET /api/v1/system/status", s.auth(s.handleSystemStatus))
	mux.HandleFunc("GET /api/v1/image", s.auth(s.handleImage))
	mux.HandleFunc("DELETE /api/v1/cache", s.requireAdmin(s.handleClearAllCache))
	mux.HandleFunc("GET /api/v1/backup", s.requireAdmin(s.handleListBackups))
	mux.HandleFunc("POST /api/v1/backup", s.requireAdmin(s.handleCreateBackup))
	mux.HandleFunc("DELETE /api/v1/backup/{name}", s.requireAdmin(s.handleDeleteBackup))
	mux.HandleFunc("POST /api/v1/backup/{name}/restore", s.requireAdmin(s.handleRestoreBackup))
	mux.HandleFunc("GET /api/v1/backup/{name}/download", s.requireAdmin(s.handleDownloadBackup))
	mux.HandleFunc("GET /api/v1/health", s.auth(s.handleHealth))
	mux.HandleFunc("POST /api/v1/health/check", s.auth(s.handleHealthCheck))
	mux.HandleFunc("GET /api/v1/log", s.requireAdmin(s.handleLogTail))
	mux.HandleFunc("GET /api/v1/filesystem", s.requireAdmin(s.handleBrowseFilesystem))
	mux.HandleFunc("GET /api/v1/rootfolder", s.requireAdmin(s.handleListRootFolders))
	mux.HandleFunc("POST /api/v1/rootfolder", s.requireAdmin(s.handleAddRootFolder))
	mux.HandleFunc("DELETE /api/v1/rootfolder/{id}", s.requireAdmin(s.handleDeleteRootFolder))

	// Music: the only media-type domain left — see internal/musiclibrary's
	// package doc comment.
	mux.HandleFunc("GET /api/v1/music/artist", s.auth(s.handleListMusicArtists))
	mux.HandleFunc("GET /api/v1/music/artist/search", s.auth(s.handleSearchMusicArtists))
	mux.HandleFunc("POST /api/v1/music/artist", s.auth(s.handleMonitorMusicArtist))
	mux.HandleFunc("GET /api/v1/music/artist/{id}", s.auth(s.handleGetMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/unmonitor", s.auth(s.handleUnmonitorMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/refresh", s.auth(s.handleRefreshMusicArtist))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/missing", s.auth(s.handleListMissingMusicReleaseGroups))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/albums", s.auth(s.handleListMusicAlbumsByArtist))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/organize/preview", s.auth(s.handlePreviewOrganizeMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/organize", s.auth(s.handleOrganizeMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/wanted", s.auth(s.handleWantMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/wanted", s.auth(s.handleListWantedMusicAlbums))
	mux.HandleFunc("DELETE /api/v1/music/artist/{id}", s.auth(s.handleRemoveMusicArtist))
	mux.HandleFunc("GET /api/v1/music/album/{id}", s.auth(s.handleGetMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/album/{id}/tracks", s.auth(s.handleListMusicTracksByAlbum))
	// Not requireAdmin/plain auth header only: an <img src> can't attach a
	// header, so covers ride the API key via ?apikey= — handled by s.auth
	// already accepting the query form (see apiKeyMatches).
	mux.HandleFunc("GET /api/v1/music/album/{id}/cover", s.auth(s.handleMusicAlbumCover))
	mux.HandleFunc("GET /api/v1/music/track/{id}/files", s.auth(s.handleListMusicTrackFilesByTrack))
	mux.HandleFunc("GET /api/v1/music/trackfile/unmatched", s.auth(s.handleListUnmatchedTrackFiles))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/match", s.auth(s.handleManualMatchTrackFile))
	mux.HandleFunc("DELETE /api/v1/music/trackfile/{id}/match", s.auth(s.handleClearTrackFileMatch))
	mux.HandleFunc("GET /api/v1/music/trackfile/{id}/organize/preview", s.auth(s.handlePreviewOrganizeTrackFile))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/organize", s.auth(s.handleOrganizeTrackFile))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/write-tags", s.auth(s.handleWriteMusicTags))
	mux.HandleFunc("DELETE /api/v1/music/trackfile/{id}", s.auth(s.handleDeleteTrackFile))
	mux.HandleFunc("GET /api/v1/music/musicbrainz/search", s.auth(s.handleSearchMusicBrainzRecordings))
	mux.HandleFunc("GET /api/v1/music/releasegroup/{mbid}/tracks", s.auth(s.handleGetReleaseGroupTracklist))
	mux.HandleFunc("POST /api/v1/music/scan", s.auth(s.handleTriggerMusicScan))
	mux.HandleFunc("GET /api/v1/music/scan/status", s.auth(s.handleMusicScanStatus))
	mux.HandleFunc("POST /api/v1/music/wanted/{id}/ignore", s.auth(s.handleIgnoreWantedMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/wanted/{id}/search", s.auth(s.handleSearchWantedMusicAlbum))
	mux.HandleFunc("POST /api/v1/music/wanted/{id}/grab", s.auth(s.handleGrabWantedMusicAlbum))

	mux.HandleFunc("GET /api/v1/settings/naming", s.requireAdmin(s.handleGetNamingSettings))
	mux.HandleFunc("PUT /api/v1/settings/naming", s.requireAdmin(s.handlePutNamingSettings))
	mux.HandleFunc("GET /api/v1/settings/music", s.requireAdmin(s.handleGetMusicSettings))
	mux.HandleFunc("PUT /api/v1/settings/music", s.requireAdmin(s.handlePutMusicSettings))
	mux.HandleFunc("GET /api/v1/settings/timings", s.requireAdmin(s.handleGetTimingSettings))
	mux.HandleFunc("PUT /api/v1/settings/timings", s.requireAdmin(s.handlePutTimingSettings))
	mux.HandleFunc("GET /api/v1/settings/pathmappings", s.requireAdmin(s.handleGetPathMappings))
	mux.HandleFunc("PUT /api/v1/settings/pathmappings", s.requireAdmin(s.handlePutPathMappings))

	mux.HandleFunc("GET /api/v1/qualityprofile", s.requireAdmin(s.handleListProfiles))
	mux.HandleFunc("POST /api/v1/qualityprofile", s.requireAdmin(s.handleAddProfile))
	mux.HandleFunc("PUT /api/v1/qualityprofile/{id}", s.requireAdmin(s.handleUpdateProfile))
	mux.HandleFunc("PUT /api/v1/qualityprofile/{id}/default", s.requireAdmin(s.handleDefaultProfile))
	mux.HandleFunc("DELETE /api/v1/qualityprofile/{id}", s.requireAdmin(s.handleDeleteProfile))

	mux.HandleFunc("GET /api/v1/indexer", s.requireAdmin(s.handleListIndexers))
	mux.HandleFunc("POST /api/v1/indexer", s.requireAdmin(s.handleAddIndexer))
	mux.HandleFunc("GET /api/v1/indexer/schema", s.requireAdmin(s.handleIndexerSchema))
	mux.HandleFunc("GET /api/v1/indexer/native", s.requireAdmin(s.handleListNativeIndexers))
	mux.HandleFunc("GET /api/v1/indexer/{id}", s.requireAdmin(s.handleGetIndexer))
	mux.HandleFunc("PUT /api/v1/indexer/{id}", s.requireAdmin(s.handleUpdateIndexer))
	mux.HandleFunc("DELETE /api/v1/indexer/{id}", s.requireAdmin(s.handleDeleteIndexer))
	mux.HandleFunc("POST /api/v1/indexer/test", s.requireAdmin(s.handleTestIndexer))
	mux.HandleFunc("GET /api/v1/tag", s.requireAdmin(s.handleListTags))
	// Readarr-only capability Prowlarr reads during app sync (see handler).
	mux.HandleFunc("GET /api/v1/metadataprofile", s.requireAdmin(s.handleListMetadataProfiles))

	mux.HandleFunc("GET /api/v1/downloadclient", s.requireAdmin(s.handleListDownloadClients))
	mux.HandleFunc("POST /api/v1/downloadclient", s.requireAdmin(s.handleAddDownloadClient))
	mux.HandleFunc("PUT /api/v1/downloadclient/{id}", s.requireAdmin(s.handleUpdateDownloadClient))
	mux.HandleFunc("DELETE /api/v1/downloadclient/{id}", s.requireAdmin(s.handleDeleteDownloadClient))
	mux.HandleFunc("POST /api/v1/downloadclient/test", s.requireAdmin(s.handleTestDownloadClient))
	mux.HandleFunc("GET /api/v1/queue", s.auth(s.handleQueue))
	mux.HandleFunc("DELETE /api/v1/queue/{id}/{itemId}", s.auth(s.handleRemoveQueueItem))
	mux.HandleFunc("GET /api/v1/blocklist", s.auth(s.handleBlocklist))
	mux.HandleFunc("DELETE /api/v1/blocklist/{id}", s.auth(s.handleUnblock))
	mux.HandleFunc("GET /api/v1/history", s.auth(s.handleHistory))
	mux.HandleFunc("POST /api/v1/grab/{id}/cancel", s.auth(s.handleCancelGrab))

	mux.HandleFunc("/", s.handleIndex)

	return logRequests(mux), &Background{Health: s.health}
}

// handleHealth returns the cached result of the last background health run
// (checkedAt is the zero time before the first run completes).
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.health.Last())
}

// handleHealthCheck re-runs every check now — the System page's button.
func (s *server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.health.Check(r.Context()))
}

// refreshHealth re-runs the health checks in the background after a change
// that can raise or resolve an issue (indexer/download-client/root-folder
// edits — including Prowlarr's sync writes), so the warning banner updates
// without waiting for the 15-minute tick.
func (s *server) refreshHealth() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.health.Check(ctx)
	}()
}

// auth admits requests carrying the API key (scripts, Prowlarr) or a valid
// login session cookie (the web UI once authentication is enabled) — either
// role. Use requireAdmin instead for the server's own configuration.
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKeyMatches(r) || s.hasSession(r) {
			next(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid or missing API key")
	}
}

// requireAdmin is auth, plus a role check: the API key always passes (it's
// the instance owner's master credential — scripts and Prowlarr authenticate
// this way, and have no narrower role to check), but a session belonging to
// a member account is turned away. Everything that touches the server's own
// configuration — settings, indexers, download clients, backups, logs, user
// management — sits behind this instead of auth.
func (s *server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKeyMatches(r) {
			next(w, r)
			return
		}
		if sess, ok := s.sessions.lookup(currentToken(r)); ok {
			if sess.role == config.RoleAdmin {
				next(w, r)
				return
			}
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid or missing API key")
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
