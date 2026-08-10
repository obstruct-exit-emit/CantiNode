// Package importer polls in-flight grabs against their download clients
// and, once one completes, copies its files into the music library and
// scans them in — the automatic half of CantiNode's acquisition pipeline.
// Grabbing itself is always a manual, human choice (see internal/download);
// this is what happens after, so a finished download doesn't just sit in
// the download client's folder until someone remembers to go get it.
package importer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
)

// PollInterval is how often in-flight grabs are checked against their
// download clients — independent of (and much shorter than) a library
// scan, since a user watching a grab wants responsive feedback. Not
// currently exposed as a setting: a fixed, reasonable default rather than
// one more knob.
const PollInterval = 2 * time.Minute

// Service ties the download queue, the configured root folders, and the
// music scanner together.
type Service struct {
	downloads *download.Service
	scanner   *musicscanner.Scanner
	music     *musiclibrary.Store
	cfg       *config.Config
	logger    *slog.Logger
}

func New(downloads *download.Service, scanner *musicscanner.Scanner, music *musiclibrary.Store, cfg *config.Config) *Service {
	return &Service{downloads: downloads, scanner: scanner, music: music, cfg: cfg, logger: slog.Default()}
}

// RunPeriodic polls immediately (so a fresh start doesn't wait a full
// interval to catch up on anything already finished), then again every
// interval until ctx is canceled. interval <= 0 uses PollInterval.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = PollInterval
	}
	s.PollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollOnce(ctx)
		}
	}
}

// PollResult summarizes one PollOnce pass, for logging/testing.
type PollResult struct {
	Checked  int
	Imported int
	Failed   int
}

// PollOnce checks every "grabbed" (in-flight) grab against its download
// client's current queue and acts on whichever ones have moved on: a
// completed/seeded item gets imported (see importGrab), a failed one is
// recorded as failed, and one that's vanished from its client's queue
// entirely — without that client having simply failed to answer this pass
// — is treated the same way (removed directly in the client, or lost to a
// client restart).
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult

	grabs, err := s.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		s.logger.Error("importer: list grabbed downloads", "error", err)
		return result
	}
	if len(grabs) == 0 {
		return result
	}

	items, _, err := s.downloads.Queue(ctx)
	if err != nil {
		s.logger.Error("importer: queue downloads", "error", err)
		return result
	}
	failedClients := s.downloads.FailedClients()

	byKey := make(map[string]download.Item, len(items))
	for _, it := range items {
		byKey[queueKey(it.ConfigID, it.ID)] = it
	}

	for _, g := range grabs {
		result.Checked++
		item, ok := byKey[queueKey(g.ClientConfigID, g.ClientItemID)]
		if !ok {
			if failedClients[g.ClientConfigID] {
				continue // that client just didn't answer this pass — not a true orphan
			}
			if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed,
				"no longer in the download client's queue (removed there, or lost to a client restart)"); err != nil {
				s.logger.Error("importer: resolve orphaned grab", "grab_id", g.ID, "error", err)
				continue
			}
			result.Failed++
			continue
		}
		switch item.Status {
		case "completed", "seeded":
			if s.importGrab(ctx, g, item) {
				result.Imported++
			} else {
				result.Failed++
			}
		case "failed":
			msg := "download client reported it failed"
			if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed, msg); err != nil {
				s.logger.Error("importer: resolve failed grab", "grab_id", g.ID, "error", err)
				continue
			}
			result.Failed++
		}
	}
	return result
}

func queueKey(configID int64, itemID string) string {
	return fmt.Sprintf("%d:%s", configID, itemID)
}

// importGrab copies a completed download's files from item.Path (the
// download client's own local disk, translated through the configured
// remote path mappings — see config.TranslatePath) into the first
// configured root folder, then scans that root so the copied files are
// matched/organized the normal way, same as any other file dropped there.
// A copy, not a move: the download client keeps its own copy under its own
// retention policy, which CantiNode has no business overriding. Reports
// whether the grab ended up imported.
func (s *Service) importGrab(ctx context.Context, g download.GrabRecord, item download.Item) bool {
	if item.Path == "" {
		s.logger.Warn("importer: completed download reported no path, leaving it for manual import",
			"grab_id", g.ID, "title", g.Title)
		return false
	}
	folders, err := s.music.ListRootFolders()
	if err != nil || len(folders) == 0 {
		s.logger.Error("importer: no music root folder configured, leaving grab for manual import", "grab_id", g.ID)
		return false
	}
	root := folders[0]

	src := config.TranslatePath(s.cfg.PathMappings(), item.Path)
	dest := filepath.Join(root.Path, filepath.Base(src))
	if err := copyTree(src, dest); err != nil {
		msg := fmt.Sprintf("copy from download client failed: %v", err)
		s.logger.Error("importer: copy failed", "grab_id", g.ID, "src", src, "dest", dest, "error", err)
		if rerr := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusFailed, msg); rerr != nil {
			s.logger.Error("importer: resolve failed grab", "grab_id", g.ID, "error", rerr)
		}
		return false
	}

	if _, err := s.scanner.ScanAll(ctx); err != nil {
		s.logger.Warn("importer: post-import scan failed, the next scan will still pick these files up",
			"grab_id", g.ID, "error", err)
	}
	if err := s.downloads.Store().ResolveGrab(g.ID, download.GrabStatusImported, ""); err != nil {
		s.logger.Error("importer: resolve imported grab", "grab_id", g.ID, "error", err)
		return false
	}
	s.logger.Info("importer: imported completed download", "grab_id", g.ID, "title", g.Title, "dest", dest)
	return true
}

// copyTree copies src (a file or a directory, recursively) into dstDir,
// preserving relative structure for a directory.
func copyTree(src, dstDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", src, err)
	}
	if !info.IsDir() {
		return copyFile(src, filepath.Join(dstDir, filepath.Base(src)))
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read source dir %s: %w", src, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dstDir, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy to %s: %w", dst, err)
	}
	return out.Close()
}
