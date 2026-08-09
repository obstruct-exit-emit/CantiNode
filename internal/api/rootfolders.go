package api

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strconv"
)

var mediaTypes = []string{"ebook", "audiobook", "manga", "comic", "magazine"}

// mangaVariants are the colorized/monochrome sub-libraries a manga root can
// hold; other media types have no variant ("").
var mangaVariants = []string{"color", "mono"}

type rootFolder struct {
	ID         int64  `json:"id"`
	MediaType  string `json:"mediaType"`
	Variant    string `json:"variant,omitempty"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	CreatedAt  string `json:"createdAt"`
}

func (s *server) handleListRootFolders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT id, media_type, variant, path, created_at FROM root_folders ORDER BY media_type, variant, path`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	folders := []rootFolder{}
	for rows.Next() {
		var f rootFolder
		if err := rows.Scan(&f.ID, &f.MediaType, &f.Variant, &f.Path, &f.CreatedAt); err != nil {
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
	// Prowlarr's app sync reads root folders as Readarr resources; serve it
	// the Readarr-shaped view (the browser UI keeps its native shape).
	if isProwlarr(r) {
		out := make([]map[string]any, 0, len(folders))
		for _, f := range folders {
			out = append(out, readarrRootFolder(f))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (s *server) handleAddRootFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType string `json:"mediaType"`
		Variant   string `json:"variant"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !slices.Contains(mediaTypes, req.MediaType) {
		writeError(w, http.StatusBadRequest, "mediaType must be one of: ebook, audiobook, manga, comic, magazine")
		return
	}
	// Manga roots are per-variant (colorized/monochrome share one library);
	// monochrome is the default. Every other media type has no variant.
	if req.MediaType == "manga" {
		if req.Variant == "" {
			req.Variant = "mono"
		}
		if !slices.Contains(mangaVariants, req.Variant) {
			writeError(w, http.StatusBadRequest, "variant must be color or mono for manga roots")
			return
		}
	} else {
		req.Variant = ""
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if !dirExists(req.Path) {
		writeError(w, http.StatusBadRequest, "path does not exist or is not a directory")
		return
	}

	res, err := s.db.Exec(`INSERT INTO root_folders (media_type, variant, path) VALUES (?, ?, ?)`,
		req.MediaType, req.Variant, req.Path)
	if err != nil {
		writeError(w, http.StatusConflict, "folder already added or could not be saved: "+err.Error())
		return
	}
	id, _ := res.LastInsertId()

	var f rootFolder
	err = s.db.QueryRow(`SELECT id, media_type, variant, path, created_at FROM root_folders WHERE id = ?`, id).
		Scan(&f.ID, &f.MediaType, &f.Variant, &f.Path, &f.CreatedAt)
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
