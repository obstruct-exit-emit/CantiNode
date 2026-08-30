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
			}
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
