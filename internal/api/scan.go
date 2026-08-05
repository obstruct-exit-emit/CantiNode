package api

import (
	"fmt"
	"net/http"
	"time"
)

// handleTriggerScan starts a full scan (every root folder) in the
// background and returns immediately — a real scan can take a while
// (MusicBrainz is rate-limited to ~1 request/sec, so a library with
// hundreds of unmatched files takes minutes, not seconds), so this
// can't be a synchronous request/response. Poll GET /api/v1/scan/status
// for progress. Refuses to start a second scan while one is already
// running rather than letting two walk the same root folders at once.
func (s *Server) handleTriggerScan(w http.ResponseWriter, r *http.Request) {
	s.scanMu.Lock()
	if s.scanState.Running {
		s.scanMu.Unlock()
		writeError(w, http.StatusConflict, fmt.Errorf("a scan is already running"))
		return
	}
	now := time.Now().UTC()
	s.scanState = scanState{Running: true, StartedAt: &now}
	s.scanMu.Unlock()

	go func() {
		ctx, cancel := scanContext()
		defer cancel()

		result, err := s.scanner.ScanAll(ctx)

		s.scanMu.Lock()
		finished := time.Now().UTC()
		s.scanState.Running = false
		s.scanState.FinishedAt = &finished
		s.scanState.Result = result
		if err != nil {
			s.scanState.Error = err.Error()
		}
		s.scanMu.Unlock()
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": "started"})
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	s.scanMu.Lock()
	state := s.scanState
	s.scanMu.Unlock()
	writeJSON(w, state)
}
