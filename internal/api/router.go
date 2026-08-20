// Package api exposes CantiNode's versioned REST API and serves the web UI.
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

	"github.com/cantinode/cantinode/internal/audiodb"
	"github.com/cantinode/cantinode/internal/autosearch"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/coverart"
	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/discoveryrefresh"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/health"
	"github.com/cantinode/cantinode/internal/imagecache"
	"github.com/cantinode/cantinode/internal/importer"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
	"github.com/cantinode/cantinode/internal/metadatabackfill"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
	"github.com/cantinode/cantinode/web"
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
	musicStore       *musiclibrary.Store
	musicScanner     *musicscanner.Scanner
	mb               *musicbrainz.Client
	audiodb          *audiodb.Client
	coverart         *coverart.Client
	discography      *discography.Service
	metadataBackfill *metadatabackfill.Service

	musicScanMu    sync.Mutex
	musicScanState musicScanState

	musicMoveMu    sync.Mutex
	musicMoveState musicMoveState
}

// Background bundles the services main runs on periodic loops.
type Background struct {
	Health           *health.Service
	Importer         *importer.Service
	Autosearch       *autosearch.Service
	DiscoveryRefresh *discoveryrefresh.Service
	MetadataBackfill *metadatabackfill.Service
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
	// One shared client for both artist metadata and cover art — two
	// independent clients would each keep their own throttle state,
	// doubling the effective request rate against TheAudioDB.
	audiodbClient := audiodb.NewClient(musicSettings.AudioDBAPIKey)
	discographySvc := discography.New(mb, musicStore)
	metadataBackfillSvc := metadatabackfill.New(musicStore, mb, audiodbClient, discographySvc)

	s := &server{
		cfg:              cfg,
		db:               db,
		store:            store,
		indexers:         indexers,
		downloads:        downloads,
		health:           health.New(store, indexers, downloads),
		images:           imagecache.New(filepath.Join(cfg.DataDir(), "covers", "remote")),
		sessions:         newSessionStore(),
		version:          version,
		musicStore:       musicStore,
		musicScanner:     musicScanner,
		mb:               mb,
		audiodb:          audiodbClient,
		coverart:         coverart.NewClient(filepath.Join(cfg.DataDir(), "covers", "music"), "CantiNode/"+version, audiodbClient),
		discography:      discographySvc,
		metadataBackfill: metadataBackfillSvc,
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
	mux.HandleFunc("PUT /api/v1/rootfolder/{id}/name", s.requireAdmin(s.handleRenameRootFolder))
	mux.HandleFunc("PUT /api/v1/rootfolder/{id}/default", s.requireAdmin(s.handleSetDefaultRootFolder))

	// Music: the only media-type domain left — see internal/musiclibrary's
	// package doc comment.
	mux.HandleFunc("GET /api/v1/music/artist", s.auth(s.handleListMusicArtists))
	mux.HandleFunc("GET /api/v1/music/artist/search", s.auth(s.handleSearchMusicArtists))
	mux.HandleFunc("POST /api/v1/music/artist", s.auth(s.handleMonitorMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/quick", s.auth(s.handleQuickAddMusicArtist))
	mux.HandleFunc("POST /api/v1/music/series", s.auth(s.handleAddMusicSeries))
	mux.HandleFunc("GET /api/v1/music/artist/{id}", s.auth(s.handleGetMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/unmonitor", s.auth(s.handleUnmonitorMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/refresh", s.auth(s.handleRefreshMusicArtist))
	// Not requireAdmin/plain auth header only: opened as a plain browser
	// navigation (target="_blank"), same ?apikey= reasoning as the album
	// cover/audiodb-link routes.
	mux.HandleFunc("GET /api/v1/music/artist/{id}/audiodb-link", s.auth(s.handleAudioDBArtistLink))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/missing", s.auth(s.handleListMissingMusicReleaseGroups))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/albums", s.auth(s.handleListMusicAlbumsByArtist))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/organize/preview", s.auth(s.handlePreviewOrganizeMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/organize", s.auth(s.handleOrganizeMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/write-tags", s.auth(s.handleWriteMusicTagsForArtist))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/move/preview", s.auth(s.handlePreviewMoveMusicArtist))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/move", s.auth(s.handleMoveMusicArtist))
	mux.HandleFunc("GET /api/v1/music/move/status", s.auth(s.handleMusicMoveStatus))
	mux.HandleFunc("POST /api/v1/music/artist/{id}/wanted", s.auth(s.handleWantMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/artist/{id}/wanted", s.auth(s.handleListWantedMusicAlbums))
	mux.HandleFunc("DELETE /api/v1/music/artist/{id}", s.auth(s.handleRemoveMusicArtist))
	mux.HandleFunc("GET /api/v1/music/album/{id}", s.auth(s.handleGetMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/album/{id}/tracks", s.auth(s.handleListMusicTracksByAlbum))
	// Not requireAdmin/plain auth header only: an <img src> can't attach a
	// header, so covers ride the API key via ?apikey= — handled by s.auth
	// already accepting the query form (see apiKeyMatches).
	mux.HandleFunc("GET /api/v1/music/album/{id}/cover", s.auth(s.handleMusicAlbumCover))
	// Not requireAdmin/plain auth header only: opened as a plain browser
	// navigation (target="_blank"), same ?apikey= reasoning as the cover
	// route above.
	mux.HandleFunc("GET /api/v1/music/album/{id}/audiodb-link", s.auth(s.handleAudioDBAlbumLink))
	mux.HandleFunc("GET /api/v1/music/album/{id}/description", s.auth(s.handleGetMusicAlbumDescription))
	mux.HandleFunc("GET /api/v1/music/album/{id}/organize/preview", s.auth(s.handlePreviewOrganizeMusicAlbum))
	mux.HandleFunc("POST /api/v1/music/album/{id}/organize", s.auth(s.handleOrganizeMusicAlbum))
	mux.HandleFunc("POST /api/v1/music/album/{id}/write-tags", s.auth(s.handleWriteMusicTagsForAlbum))
	mux.HandleFunc("POST /api/v1/music/album/{id}/scan", s.auth(s.handleScanMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/album/{id}/upgrade/search", s.auth(s.handleSearchAlbumUpgrade))
	mux.HandleFunc("POST /api/v1/music/album/{id}/upgrade/grab", s.auth(s.handleGrabAlbumUpgrade))
	mux.HandleFunc("DELETE /api/v1/music/album/{id}", s.auth(s.handleRemoveMusicAlbum))
	mux.HandleFunc("GET /api/v1/music/track/{id}/files", s.auth(s.handleListMusicTrackFilesByTrack))
	mux.HandleFunc("GET /api/v1/music/trackfile/unmatched", s.auth(s.handleListUnmatchedTrackFiles))
	mux.HandleFunc("POST /api/v1/music/trackfile/match-suggest", s.auth(s.handleSuggestTrackFileMatches))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/match", s.auth(s.handleManualMatchTrackFile))
	mux.HandleFunc("DELETE /api/v1/music/trackfile/{id}/match", s.auth(s.handleClearTrackFileMatch))
	mux.HandleFunc("GET /api/v1/music/trackfile/{id}/organize/preview", s.auth(s.handlePreviewOrganizeTrackFile))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/organize", s.auth(s.handleOrganizeTrackFile))
	mux.HandleFunc("POST /api/v1/music/trackfile/{id}/write-tags", s.auth(s.handleWriteMusicTags))
	mux.HandleFunc("DELETE /api/v1/music/trackfile/{id}", s.auth(s.handleDeleteTrackFile))
	mux.HandleFunc("GET /api/v1/music/musicbrainz/search", s.auth(s.handleSearchMusicBrainzRecordings))
	mux.HandleFunc("GET /api/v1/music/releasegroup/{mbid}/tracks", s.auth(s.handleGetReleaseGroupTracklist))
	mux.HandleFunc("GET /api/v1/music/releasegroup/{mbid}/versions", s.auth(s.handleListReleaseGroupVersions))
	mux.HandleFunc("GET /api/v1/music/releasegroup/{mbid}/cover", s.auth(s.handleReleaseGroupCover))
	mux.HandleFunc("POST /api/v1/music/scan", s.auth(s.handleTriggerMusicScan))
	mux.HandleFunc("GET /api/v1/music/scan/status", s.auth(s.handleMusicScanStatus))
	mux.HandleFunc("DELETE /api/v1/music/wanted/{id}", s.auth(s.handleRemoveWantedMusicAlbum))
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
	mux.HandleFunc("GET /api/v1/indexer/native", s.requireAdmin(s.handleListNativeIndexers))
	mux.HandleFunc("GET /api/v1/indexer/{id}", s.requireAdmin(s.handleGetIndexer))
	mux.HandleFunc("PUT /api/v1/indexer/{id}", s.requireAdmin(s.handleUpdateIndexer))
	mux.HandleFunc("DELETE /api/v1/indexer/{id}", s.requireAdmin(s.handleDeleteIndexer))
	mux.HandleFunc("POST /api/v1/indexer/test", s.requireAdmin(s.handleTestIndexer))

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

	imp := importer.New(downloads, musicScanner, musicStore, cfg)
	auto := autosearch.New(musicStore, indexers, downloads, store)
	discoveryRefresh := discoveryrefresh.New(musicStore, discographySvc)

	return logRequests(mux), &Background{Health: s.health, Importer: imp, Autosearch: auto, DiscoveryRefresh: discoveryRefresh, MetadataBackfill: metadataBackfillSvc}
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
// edits), so the warning banner updates without waiting for the 15-minute
// tick.
func (s *server) refreshHealth() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.health.Check(ctx)
	}()
}

// auth admits requests carrying the API key (scripts) or a valid
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
// the instance owner's master credential — scripts authenticate this way,
// and have no narrower role to check), but a session belonging to
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
