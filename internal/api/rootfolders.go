package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func (s *Server) handleListRootFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := s.db.ListRootFolders(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, folders)
}

type createRootFolderRequest struct {
	Path string `json:"path"`
}

// handleCreateRootFolder adds a root folder. The path must already exist
// on disk — CantiNode organizes an existing library, it doesn't create
// one from nothing, so a typo'd path is caught here instead of silently
// scanning zero files forever.
func (s *Server) handleCreateRootFolder(w http.ResponseWriter, r *http.Request) {
	var req createRootFolderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path must not be empty"))
		return
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path %q is not accessible: %w", req.Path, err))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path %q is not a directory", req.Path))
		return
	}

	rf, err := s.db.CreateRootFolder(r.Context(), req.Path)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, rf)
}

func (s *Server) handleDeleteRootFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteRootFolder(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
