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
	"sync"
	"syscall"
	"time"

	"github.com/cantinode/cantinode/internal/api"
	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/importer"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/indexer/prowlarr"
	"github.com/cantinode/cantinode/internal/logging"
	"github.com/cantinode/cantinode/internal/metadatabackfill"
	"github.com/cantinode/cantinode/internal/plexplaylistsync"
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
	// (see internal/importer), autosearch sweeping monitored artists'
	// wanted albums to search and grab automatically (see
	// internal/autosearch), discoveryrefresh re-caching every monitored
	// artist's own discography so a new release lands in Missing on its own
	// (see internal/discoveryrefresh), metadatabackfill catching
	// up any artist still missing discography/bio/photo metadata — normally
	// finished inline right after a scan, but restart-safe against an
	// interruption mid-sweep since it also runs independently on its own
	// timer (see internal/metadatabackfill) — importlist resolving every
	// enabled import list (a MusicBrainz Series, a plain artist list, or a
	// Last.fm user/tag) to add and monitor any newly-appearing artist (see
	// internal/importlist) — and plexplaylistsync keeping a linked Plex
	// server's playlists and CantiNode's own in sync both ways, when
	// Settings → Integrations has playlist sync turned on (see
	// internal/plexplaylistsync).
	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	// Cadences: built-in defaults unless tuned under Settings → General →
	// Background timings (applied at startup — a change needs a restart).
	timings := cfg.TimingSettings()

	handler, bg := api.NewRouter(cfg, db, version)
	// bgWG is joined on shutdown (below) so the process doesn't just exit
	// out from under whichever loop is mid-PollOnce — found live: nothing
	// previously waited for these at all, so a SIGTERM/container-stop
	// arriving while the importer was mid-copy of a finished grab's audio
	// files into the library (internal/importer's copyTree/copyFile, which
	// have no cancellation of their own) raced the process exit against
	// that write, risking a truncated file left in the library with no
	// record it never finished. cancelBg still only stops a loop from
	// starting its *next* pass — it was already the only lever available
	// for an in-progress one — but now something actually gives the
	// current pass a real chance to finish before main() returns, instead
	// of none at all.
	var bgWG sync.WaitGroup
	// runBG also recovers a panic anywhere inside fn — found live: grepping
	// the whole production tree turned up zero recover() calls, so a panic
	// anywhere in a loop's own call chain (indexer parsing, release scoring,
	// candidatesearch, a download client) would take down this bare
	// goroutine AND, since nothing stopped it, the entire process — the
	// HTTP server and every other unrelated background loop included —
	// over one bad release title or a single artist's malformed data. A
	// panicking loop is logged with its stack and restarted after a short
	// backoff instead, same spirit as RunPeriodic's own "sweep immediately
	// on (re)start" behavior already relies on for resuming after an
	// ordinary process restart.
	runBG := func(name string, fn func(ctx context.Context)) {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			for {
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.Error("background loop panicked, restarting",
								"loop", name, "panic", r, "stack", string(debug.Stack()))
						}
					}()
					fn(bgCtx)
				}()
				if bgCtx.Err() != nil {
					return
				}
				select {
				case <-bgCtx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
	// Paces autosearch's own per-album searches within one sweep — see
	// Service.InterAlbumDelay's own doc comment. Set once here, before
	// RunPeriodic's goroutine (below) ever starts reading it.
	bg.Autosearch.InterAlbumDelay = autosearchInterAlbumDelay

	runBG("health", func(ctx context.Context) { bg.Health.RunPeriodic(ctx, timings.HealthInterval()) })
	runBG("importer", func(ctx context.Context) { bg.Importer.RunPeriodic(ctx, importer.PollInterval) })
	runBG("autosearch", func(ctx context.Context) { bg.Autosearch.RunPeriodic(ctx, timings.WantedSearchNextRun) })
	runBG("discoveryrefresh", func(ctx context.Context) {
		bg.DiscoveryRefresh.RunPeriodic(ctx, func(now time.Time) time.Time {
			return now.Add(timings.DiscographyRefreshInterval())
		})
	})
	runBG("metadatabackfill", func(ctx context.Context) { bg.MetadataBackfill.RunPeriodic(ctx, metadatabackfill.PollInterval) })
	runBG("importlist", func(ctx context.Context) {
		bg.ImportLists.RunPeriodic(ctx, func(now time.Time) time.Time {
			return now.Add(timings.ImportListSyncInterval())
		})
	})
	runBG("plexplaylistsync", func(ctx context.Context) { bg.PlexPlaylistSync.RunPeriodic(ctx, plexplaylistsync.PollInterval) })

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
		shutdownErr := srv.Shutdown(ctx)

		// Signal every background loop to stop scheduling further work, then
		// give whichever one is mid-pass a real grace period to finish
		// before returning — see runBG's own comment above for why this
		// matters. A loop that's hung (not just slow) only delays this
		// return by bgShutdownGrace, it never blocks it forever.
		cancelBg()
		bgDone := make(chan struct{})
		go func() {
			bgWG.Wait()
			close(bgDone)
		}()
		select {
		case <-bgDone:
		case <-time.After(bgShutdownGrace):
			logger.Warn("background loops still running past shutdown grace period, exiting anyway")
		}
		return shutdownErr
	}
}

// bgShutdownGrace bounds how long shutdown waits for an in-flight background
// pass (e.g. the importer mid-copy of a finished grab's files) to finish on
// its own before giving up and exiting anyway.
const bgShutdownGrace = 30 * time.Second

// autosearchInterAlbumDelay paces the wanted-list sweep's own per-album
// searches — see autosearch.Service.InterAlbumDelay's own doc comment for
// why. A couple of seconds is cheap against PollInterval's 24h default and
// comfortably bounds even a large backlog (a few hundred albums adds under
// ten minutes total, once, not on any user-visible path) while giving a
// per-minute-limited indexer real breathing room between requests.
const autosearchInterAlbumDelay = 2 * time.Second

// applyPendingRestore swaps staged *.restore files (written by the backup
// restore endpoint) into place, keeping the replaced files as *.pre-restore.
func applyPendingRestore(dataDir string) error {
	for _, name := range []string{"config.yaml", "cantinode.db"} {
		staged := filepath.Join(dataDir, name+".restore")
		if _, err := os.Stat(staged); err != nil {
			continue
		}

		if name == "cantinode.db" {
			// Validate before touching the live file at all. Found live:
			// restoreBackup only ever checked the surrounding zip
			// container's integrity, never the database file itself — a
			// backup corrupted by bit rot in long-term storage (or a disk
			// fault VACUUM INTO didn't catch) would otherwise get swapped
			// straight into place, and the subsequent database.Open below
			// would then fail, leaving the instance refusing to start with
			// no database at all. Open+migrate is a real integrity check
			// (SQLite errors on most corruption the moment it's queried),
			// not just a header-magic sniff. The staged file ends up
			// migrated in place either way, which is fine: it's renamed to
			// live unchanged, and the real startup Open() further down is
			// then a cheap no-op against an already-current schema.
			if err := func() error {
				db, err := database.Open(staged)
				if err != nil {
					return err
				}
				return db.Close()
			}(); err != nil {
				os.Remove(staged + "-wal")
				os.Remove(staged + "-shm")
				removePendingRestore(dataDir)
				return fmt.Errorf("staged restore database is invalid, restore aborted and discarded: %w", err)
			}
			os.Remove(staged + "-wal")
			os.Remove(staged + "-shm")
		}

		live := filepath.Join(dataDir, name)
		hadLive := false
		if _, err := os.Stat(live); err == nil {
			hadLive = true
			os.Remove(live + ".pre-restore")
			if err := os.Rename(live, live+".pre-restore"); err != nil {
				return err
			}
		}
		if err := os.Rename(staged, live); err != nil {
			// live was already moved aside above — left alone, the instance
			// would refuse to start again until an admin notices the
			// *.pre-restore file and renames it back by hand. Automatic
			// best-effort rollback instead, so a transient rename failure
			// (e.g. a brief external lock on the newly-staged file) doesn't
			// turn into a self-inflicted outage.
			if hadLive {
				os.Rename(live+".pre-restore", live)
			}
			return err
		}
		if name == "cantinode.db" {
			// A -wal/-shm left behind by an unclean shutdown belongs to the
			// pre-restore database. Left in place, SQLite would replay it
			// onto the restored file and silently reintroduce whatever the
			// restore was meant to undo.
			os.Remove(live + "-wal")
			os.Remove(live + "-shm")
		}
		slog.Info("restored from backup", "file", name)
	}
	return nil
}

// removePendingRestore discards every staged *.restore file so a restore
// that failed validation doesn't just retry (and fail) again on every
// future startup.
func removePendingRestore(dataDir string) {
	for _, name := range []string{"config.yaml", "cantinode.db"} {
		os.Remove(filepath.Join(dataDir, name+".restore"))
	}
}
