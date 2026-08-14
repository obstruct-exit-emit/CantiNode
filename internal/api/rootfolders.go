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
	rows, err := s.db.Query(`SELECT id, name, media_type, path, is_default, created_at FROM root_folders ORDER BY media_type, name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	folders := []rootFolder{}
	for rows.Next() {
		var f rootFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.MediaType, &f.Path, &f.IsDefault, &f.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Accessible = dirExists(f.Path)
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	res, err := s.db.Exec(`INSERT INTO root_folders (media_type, path, name) VALUES (?, ?, ?)`,
		req.MediaType, req.Path, name)
	if err != nil {
		writeError(w, http.StatusConflict, "folder already added or could not be saved: "+err.Error())
		return
	}
	id, _ := res.LastInsertId()

	// The very first root folder for a media type becomes its default
	// automatically — otherwise DefaultRootFolder would have nothing to
	// return until the user thought to set one by hand, breaking the
	// "new grab falls back to the default" path for a brand-new install.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM root_folders WHERE media_type = ?`, req.MediaType).Scan(&count); err == nil && count == 1 {
		s.db.Exec(`UPDATE root_folders SET is_default = 1 WHERE id = ?`, id)
	}

	var f rootFolder
	err = s.db.QueryRow(`SELECT id, name, media_type, path, is_default, created_at FROM root_folders WHERE id = ?`, id).
		Scan(&f.ID, &f.Name, &f.MediaType, &f.Path, &f.IsDefault, &f.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f.Accessible = true
	s.refreshHealth()
	writeJSON(w, http.StatusCreated, f)
}

func (s *server) handleDeleteRootFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	res, err := s.db.Exec(`DELETE FROM root_folders WHERE id = ?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "root folder not found")
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
