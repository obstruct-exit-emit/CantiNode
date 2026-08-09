package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// handleBrowseFilesystem powers the UI's folder picker (root folders in
// Settings and the setup wizard): it lists the directories under a path on
// the machine running LibriNode. Authenticated like everything else; only
// directories are returned — the picker chooses folders, never reads files.
//
// An empty path starts at the filesystem root ("/", or the drive list on
// Windows). The response carries the cleaned path, its parent ("" at the
// top), and the child directories.
func (s *server) handleBrowseFilesystem(w http.ResponseWriter, r *http.Request) {
	type dir struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}

	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		if runtime.GOOS == "windows" {
			// Drive list: the picker's top level on Windows.
			dirs := []dir{}
			for l := 'A'; l <= 'Z'; l++ {
				root := string(l) + `:\`
				if _, err := os.Stat(root); err == nil {
					dirs = append(dirs, dir{Name: root, Path: root})
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"path": "", "parent": "", "directories": dirs})
			return
		}
		p = "/"
	}

	clean := filepath.Clean(p)
	entries, err := os.ReadDir(clean)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read directory: "+err.Error())
		return
	}

	dirs := []dir{}
	for _, e := range entries {
		// Hidden directories are noise in a media-folder picker.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, dir{Name: e.Name(), Path: filepath.Join(clean, e.Name())})
	}
	sort.Slice(dirs, func(a, b int) bool {
		return strings.ToLower(dirs[a].Name) < strings.ToLower(dirs[b].Name)
	})

	parent := filepath.Dir(clean)
	if parent == clean {
		parent = "" // at the filesystem root (or drive root on Windows)
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": clean, "parent": parent, "directories": dirs})
}
