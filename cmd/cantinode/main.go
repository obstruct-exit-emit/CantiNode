// Command cantinode is a self-hosted music library organizer: it scans
// root folders of audio files, matches them against MusicBrainz, and
// organizes them into a consistent layout, with a native API and an
// embedded web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cantinode/cantinode/internal/api"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/scanner"
	"github.com/cantinode/cantinode/web"
)

// version is stamped at build time via -ldflags "-X main.version=...". A
// plain `go build` (or `go run`) without that flag keeps this default.
var version = "0.0.0-dev"

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("cantinode exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	configPath := os.Getenv("CANTINODE_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(parseLogLevel(cfg.LogLevel))
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})))

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	db, err := database.Open(cfg.DataDir + "/cantinode.db")
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	mb := musicbrainz.NewClient(version, cfg.MusicBrainzContactEmail)
	sc := scanner.New(db, mb, slog.Default(), cfg.NamingFormat, cfg.MinMatchConfidence, cfg.OrganizeOnMatch)

	// Logged so a config.yaml without an explicit api_key is still usable
	// — otherwise a randomly generated key (see internal/config) would be
	// invisible to whoever needs it to reach the API/UI from a script.
	slog.Info("api key for the native API", "api_key", cfg.APIKey)

	handler := buildHandler(db, sc, cfg, configPath)

	go runScanLoop(ctx, sc, time.Duration(cfg.ScanIntervalHours)*time.Hour)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: handler,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("cantinode starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.Info("cantinode shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// buildHandler assembles the native API and the embedded web UI under one
// *http.ServeMux on one port. Split out from run() so tests can exercise
// the full routing tree without binding a real socket.
func buildHandler(db *database.DB, sc *scanner.Scanner, cfg *config.Config, configPath string) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/api/v1/", api.NewServer(version, db, sc, cfg, configPath))

	// The embedded web UI is the lowest-priority route — it only ever
	// receives requests the API pattern above didn't claim.
	if uiHandler, err := web.Handler(); err != nil {
		slog.Error("failed to build embedded web UI handler", "error", err)
	} else {
		mux.Handle("/", uiHandler)
	}

	return mux
}

// runScanLoop runs a scan immediately (so a fresh start doesn't wait a
// full interval before the library populates), then again every interval
// until ctx is canceled. A scan error is logged, not fatal — a transient
// MusicBrainz outage shouldn't take the whole server down, and the next
// tick tries again.
func runScanLoop(ctx context.Context, sc *scanner.Scanner, interval time.Duration) {
	scanOnce := func() {
		result, err := sc.ScanAll(ctx)
		if err != nil {
			slog.Error("background scan failed", "error", err)
			return
		}
		slog.Info("background scan complete",
			"files_found", result.FilesFound,
			"files_matched", result.FilesMatched,
			"files_organized", result.FilesOrganized,
			"files_removed", result.FilesRemoved,
			"errors", len(result.Errors))
	}

	scanOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanOnce()
		}
	}
}

// parseLogLevel maps config's log_level string onto a slog.Level — config
// validates the string is one of these four (see config.Config.Validate),
// so the default case here is unreachable in practice, not a silent typo
// fallback.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
