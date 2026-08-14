package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

var mediaTypes = []string{"music"}

type rootFolder struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	MediaType  string `json:"mediaType"`
	Path       string `json:"path"`
	IsDefault  bool   `json:"isDefault"`
	Accessible bool   `json:"accessible"`
	CreatedAt  string `json:"createdAt"`
}

func (s *server) handleListRootFolders(w http.ResponseWriter, r *http.Request) {
	rfs, err := s.musicStore.ListRootFolders()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	folders := make([]rootFolder, len(rfs))
	for i, rf := range rfs {
		folders[i] = rootFolder{
			ID: rf.ID, Name: rf.Name, MediaType: "music", Path: rf.Path,
			IsDefault: rf.IsDefault, Accessible: dirExists(rf.Path), CreatedAt: rf.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, folders)
}

func (s *server) handleAddRootFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType string `json:"mediaType"`
		Path      string `json:"path"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !slices.Contains(mediaTypes, req.MediaType) {
		writeError(w, http.StatusBadRequest, "mediaType must be music")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if !dirExists(req.Path) {
		writeError(w, http.StatusBadRequest, "path does not exist or is not a directory")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		// filepath.Base of a trailing-slash path still gives the last real
		// segment (Go's filepath.Base trims trailing separators first) —
		// a reasonable default the user is always free to rename later,
		// nicer than repeating the full path as its own name.
		name = filepath.Base(req.Path)
	}

	// CreateRootFolder handles becoming the default (if no other music
	// root folder currently is one) atomically in its own transaction —
	// see its own doc comment for why that matters for two concurrent adds.
	rf, err := s.musicStore.CreateRootFolder(req.Path, name)
	if err != nil {
		writeError(w, http.StatusConflict, "folder already added or could not be saved: "+err.Error())
		return
	}

	f := rootFolder{ID: rf.ID, Name: rf.Name, MediaType: req.MediaType, Path: rf.Path, IsDefault: rf.IsDefault, Accessible: true, CreatedAt: rf.CreatedAt}
	s.refreshHealth()
	writeJSON(w, http.StatusCreated, f)
}

// handleDeleteRootFolder removes a root folder. If it was the current
// default, Store.DeleteRootFolder promotes another remaining one instead
// of silently leaving none marked — see its own doc comment.
func (s *server) handleDeleteRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicStore.DeleteRootFolder(id); err != nil {
		if errors.Is(err, musiclibrary.ErrNotFound) {
			writeError(w, http.StatusNotFound, "root folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshHealth()
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameRootFolder sets a root folder's display name — cosmetic
// only, never touches path or any on-disk file.
func (s *server) handleRenameRootFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.musicStore.RenameRootFolder(id, name); err != nil {
		if errors.Is(err, musiclibrary.ErrNotFound) {
			writeError(w, http.StatusNotFound, "root folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetDefaultRootFolder marks a root folder as the fallback
// destination for a new automatic grab that has no artist-specific folder
// of its own yet to join (see internal/importer's targetRootFolder).
func (s *server) handleSetDefaultRootFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.musicStore.SetDefaultRootFolder(id); err != nil {
		if errors.Is(err, musiclibrary.ErrNotFound) {
			writeError(w, http.StatusNotFound, "root folder not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
