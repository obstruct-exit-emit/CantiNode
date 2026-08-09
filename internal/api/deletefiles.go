package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// wantsFileDeletion reads the ?deleteFiles=true option on DELETE endpoints.
func wantsFileDeletion(r *http.Request) bool {
	return r.URL.Query().Get("deleteFiles") == "true"
}

// removeFilesFromDisk deletes library files at the given paths — the
// delete-files option on author/book/series removal. Safety: only paths
// strictly inside a configured root folder are touched. A path that is a
// directory (multi-file audiobooks) is removed recursively, and empty parent
// directories are pruned up to (never including) the root folder.
func (s *server) removeFilesFromDisk(paths []string) (deleted int, errs []string) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return 0, []string{"listing root folders: " + err.Error()}
	}
	rootOf := func(p string) string {
		for _, r := range roots {
			root := strings.TrimRight(r.Path, "/\\")
			if p != root && strings.HasPrefix(p, root+string(filepath.Separator)) {
				return root
			}
		}
		return ""
	}

	for _, p := range paths {
		root := rootOf(p)
		if root == "" {
			errs = append(errs, p+": outside every root folder — skipped")
			continue
		}
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue // already gone; nothing to count or complain about
		}
		if err == nil {
			if info.IsDir() {
				err = os.RemoveAll(p)
			} else {
				err = os.Remove(p)
			}
		}
		if err != nil {
			errs = append(errs, p+": "+err.Error())
			continue
		}
		deleted++
		// Prune now-empty parents; os.Remove refuses non-empty dirs, which
		// is exactly the stop condition.
		for dir := filepath.Dir(p); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break
			}
		}
	}
	return deleted, errs
}

// finishDelete writes the response for a DELETE that may have removed files:
// plain 204 without the option, a small JSON report with it.
func (s *server) finishDelete(w http.ResponseWriter, deleteFiles bool, paths []string) {
	if !deleteFiles {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	deleted, errs := s.removeFilesFromDisk(paths)
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deletedFiles": deleted, "errors": errs})
}
