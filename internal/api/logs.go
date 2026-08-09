package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

// handleLogTail returns the last N lines of the current log file for the
// System page's viewer. The file is size-capped by rotation (5 MB), so
// reading it whole is fine.
func (s *server) handleLogTail(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "lines must be a positive number")
			return
		}
		lines = min(n, 2000)
	}

	data, err := os.ReadFile(s.cfg.LogPath())
	if os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, map[string]any{"lines": []string{}, "path": s.cfg.LogPath()})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading log file: "+err.Error())
		return
	}

	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": all, "path": s.cfg.LogPath()})
}
