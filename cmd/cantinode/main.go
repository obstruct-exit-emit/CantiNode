package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/cantinode/cantinode/internal/api"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/importer"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/indexer/prowlarr"
	"github.com/cantinode/cantinode/internal/logging"
)

// Background cadences (wanted search, metadata refresh, health checks,
// import polling) live in config.TimingSettings — defaults there, tunable
// under Settings → General → Background timings, applied at startup.

// version is overridden at build time via -ldflags "-X main.version=x.y.z"
// (the release workflow stamps tags). Unstamped builds fall back to the git
// revision Go embeds in the binary, so even a dev build identifies itself.
var version = "dev"

// resolveVersion returns the stamped version, or derives one from the build
// info of an unstamped build: dev-<short-sha>[+dirty] (<commit-date>).
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	var rev, date, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if rev == "" {
		return version
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if len(date) > 10 {
		date = date[:10]
	}
	v := "dev-" + rev + dirty
	if date != "" {
		v += " (" + date + ")"
	}
	return v
}

func main() {
	version = resolveVersion()
	dataDir := flag.String("data", "", "path to the data directory (default: OS-specific config dir)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("CantiNode", version)
		return
	}

	if err := run(*dataDir); err != nil {
		slog.Error("cantinode exited with error", "error", err)
		os.Exit(1)
	}
}

func run(dataDir string) error {
	if dataDir == "" {
		var err error
		if dataDir, err = config.DefaultDataDir(); err != nil {
			return fmt.Errorf("resolving default data dir: %w", err)
		}
	}
	// A staged backup restore (POST /backup/{name}/restore) swaps in before
	// anything opens the config or database.
	if err := applyPendingRestore(dataDir); err != nil {
		return fmt.Errorf("applying staged restore: %w", err)
	}

	cfg, err := config.Load(dataDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Logs go to stdout and to a size-rotated file (5 MB, 3 old files kept)
	// that the UI's System → Log viewer reads back.
	logWriter := io.Writer(os.Stdout)
	if err := os.MkdirAll(filepath.Dir(cfg.LogPath()), 0o755); err == nil {
		if lf, err := logging.NewRotatingFile(cfg.LogPath(), 5<<20, 3); err == nil {
			defer lf.Close()
			logWriter = io.MultiWriter(os.Stdout, lf)
		} else {
			fmt.Fprintf(os.Stderr, "opening log file: %v (logging to stdout only)\n", err)
		}
	}
	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("starting CantiNode",
		"version", version,
		"dataDir", cfg.DataDir(),
		"listen", cfg.ListenAddr(),
	)

	db, err := database.Open(cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// Native indexer sources — selectable as an indexer "type" with no
	// Newznab/Torznab endpoint of their own. Prowlarr is registered here
	// (not scraped, unlike the framework's usual dual-use sources): it
	// searches a self-hosted Prowlarr instance directly through its own
	// API rather than CantiNode pretending to be a Readarr application
	// Prowlarr pushes indexers into.
	indexer.RegisterNative(prowlarr.Def())

	// Background loops: the periodic health check, the importer polling
	// in-flight grabs to copy a finished one into the library and scan it in
	// (see internal/importer), and autosearch sweeping monitored artists'
	// wanted albums to search and grab automatically (see
	// internal/autosearch). Metadata refresh is still triggered from the API
	// (monitor, "Refresh metadata"), not on a schedule.
	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	// Cadences: built-in defaults unless tuned under Settings → General →
	// Background timings (applied at startup — a change needs a restart).
	timings := cfg.TimingSettings()

	handler, bg := api.NewRouter(cfg, db, version)
	go bg.Health.RunPeriodic(bgCtx, timings.HealthInterval())
	go bg.Importer.RunPeriodic(bgCtx, importer.PollInterval)
	go bg.Autosearch.RunPeriodic(bgCtx, timings.WantedSearchInterval())

	srv := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("web server listening", "url", fmt.Sprintf("http://%s", cfg.ListenAddr()))
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-stop:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

// applyPendingRestore swaps staged *.restore files (written by the backup
// restore endpoint) into place, keeping the replaced files as *.pre-restore.
func applyPendingRestore(dataDir string) error {
	for _, name := range []string{"config.yaml", "cantinode.db"} {
		staged := filepath.Join(dataDir, name+".restore")
		if _, err := os.Stat(staged); err != nil {
			continue
		}
		live := filepath.Join(dataDir, name)
		if _, err := os.Stat(live); err == nil {
			os.Remove(live + ".pre-restore")
			if err := os.Rename(live, live+".pre-restore"); err != nil {
				return err
			}
		}
		if err := os.Rename(staged, live); err != nil {
			return err
		}
		slog.Info("restored from backup", "file", name)
	}
	return nil
}
