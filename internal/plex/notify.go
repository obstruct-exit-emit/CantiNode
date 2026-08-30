package plex

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/cantinode/cantinode/internal/config"
)

// NotifyPaths pushes a best-effort "refresh this path" call to Plex for
// each distinct directory among paths, translated through the configured
// path mappings (config.PlexSettings.PathMappings) — a no-op when Plex
// notification isn't enabled or isn't fully configured (server URL,
// token, and library section all required). Runs in the background and
// returns immediately: a misconfigured or slow Plex server must never add
// latency to the caller's own already-succeeded file operation (organize,
// move, import, delete) — any failure here is logged, never returned or
// retried.
//
// Every attempt is logged, success included (at Info, one line per
// directory) — not just failures. Found live: Plex's own refresh endpoint
// returns a plain 200 OK for a path it doesn't recognize at all (a typo,
// or a path mapping that's missing or wrong), the exact same response a
// real, effective refresh gets — so a silent success/no-op is
// indistinguishable from the outside without seeing the literal path this
// sent, which only a log line (not just an error, which never fires) can
// show.
func NotifyPaths(settings config.PlexSettings, logger *slog.Logger, paths []string) {
	if !settings.Enabled || settings.ServerURL == "" || settings.Token == "" || settings.SectionKey == "" {
		return
	}
	dirs := distinctDirs(paths)
	if len(dirs) == 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		client := NewClient(settings.ServerURL, settings.Token)
		for _, dir := range dirs {
			translated := config.TranslatePath(settings.PathMappings, dir)
			if err := client.RefreshPath(context.Background(), settings.SectionKey, translated); err != nil {
				logger.Warn("plex: refresh path", "path", translated, "error", err)
				continue
			}
			logger.Info("plex: refreshed path", "path", translated, "section", settings.SectionKey)
		}
	}()
}

// distinctDirs returns the distinct immediate parent directories among
// paths, in first-seen order — the notify granularity: one partial-scan
// call per directory that actually changed, rather than one per file (a
// bulk organize/move can touch dozens of files under a handful of album
// folders) or one for a whole artist's folder (which would make Plex
// rescan sibling albums that never changed).
func distinctDirs(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	dirs := make([]string, 0, len(paths))
	for _, p := range paths {
		dir := filepath.Dir(p)
		if dir == "" || dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs
}
