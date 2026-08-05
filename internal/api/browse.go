package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// browseEntry is one directory shown in a browseDirectoriesResponse.
type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// browseDirectoriesResponse is what the web UI's root-folder directory
// picker renders one level of. Parent is nil when path is already a
// top-level listing (the drive list on Windows, "/" on everything else)
// with nowhere further up to go.
type browseDirectoriesResponse struct {
	Path        string        `json:"path"`
	Parent      *string       `json:"parent"`
	Directories []browseEntry `json:"directories"`
}

// handleBrowseDirectories lists the subdirectories of ?path= (files are
// never listed — this only ever feeds a root-folder picker, not a
// general-purpose file browser) for the web UI's "Browse..." picker,
// matching the same server-side-directory-picker convention Sonarr/
// Radarr/Lidarr already use for adding root folders. An empty/absent
// path lists top-level roots instead: available drive letters on
// Windows, or "/" on every other OS.
func (s *Server) handleBrowseDirectories(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	if path == "" {
		dirs, err := listRoots()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, browseDirectoriesResponse{Path: "", Parent: nil, Directories: dirs})
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path %q is not accessible: %w", path, err))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path %q is not a directory", path))
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("read directory %q: %w", path, err))
		return
	}

	var dirs []browseEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, browseEntry{Name: e.Name(), Path: filepath.Join(path, e.Name())})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	if dirs == nil {
		dirs = []browseEntry{}
	}

	writeJSON(w, browseDirectoriesResponse{Path: path, Parent: parentOf(path), Directories: dirs})
}

// parentOf returns path's parent directory, or nil if path is already a
// top-level root (so the UI knows to show the root/drive list instead of
// trying to browse "up" from there).
func parentOf(path string) *string {
	parent := filepath.Dir(path)
	if parent == path {
		return nil
	}
	return &parent
}

// listRoots returns the top-level entries a directory picker starts
// from: every accessible drive letter on Windows, or "/" everywhere
// else.
func listRoots() ([]browseEntry, error) {
	if runtime.GOOS != "windows" {
		return []browseEntry{{Name: "/", Path: "/"}}, nil
	}

	var dirs []browseEntry
	for c := 'A'; c <= 'Z'; c++ {
		drive := string(c) + ":\\"
		if info, err := os.Stat(drive); err == nil && info.IsDir() {
			dirs = append(dirs, browseEntry{Name: string(c) + ":", Path: drive})
		}
	}
	if dirs == nil {
		dirs = []browseEntry{}
	}
	return dirs, nil
}
