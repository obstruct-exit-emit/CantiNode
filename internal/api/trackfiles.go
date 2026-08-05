package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type manualMatchRequest struct {
	RecordingMBID string `json:"recording_mbid"`
	// ReleaseMBID optionally disambiguates which of the recording's
	// releases is "the album" for this file — see
	// musicbrainz.Recording.BestRelease. Optional; falls back to the
	// recording's first release if omitted.
	ReleaseMBID string `json:"release_mbid"`
}

func (s *Server) handleManualMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req manualMatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.RecordingMBID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("recording_mbid must not be empty"))
		return
	}

	if err := s.scanner.ManualMatch(r.Context(), id, req.RecordingMBID, req.ReleaseMBID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	tf, err := s.db.GetTrackFile(r.Context(), id)
	if err != nil {
		writeError(w, notFoundStatus(err), err)
		return
	}
	writeJSON(w, tf)
}

func (s *Server) handleClearMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.scanner.ClearMatch(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePreviewOrganize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	path, err := s.scanner.PlanOrganizePath(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

func (s *Server) handleOrganize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	path, err := s.scanner.OrganizeFile(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}
