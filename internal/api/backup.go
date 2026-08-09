package api

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Backups are zips of the database (a consistent VACUUM INTO snapshot) and
// config.yaml, kept under <data>/backups. Restoring stages the files as
// *.restore next to the live ones; the next server start swaps them in
// (the previous files are kept as *.pre-restore).

var backupName = regexp.MustCompile(`^librinode-backup-\d{8}-\d{6}\.zip$`)

func (s *server) backupsDir() string {
	return filepath.Join(s.cfg.DataDir(), "backups")
}

// pathBackup validates the {name} path segment against the backup pattern —
// nothing outside the backups directory is ever addressable.
func pathBackup(r *http.Request) (string, bool) {
	name := r.PathValue("name")
	return name, backupName.MatchString(name)
}

type backupInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

func (s *server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.backupsDir())
	if err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	backups := []backupInfo{}
	for _, e := range entries {
		if !backupName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupInfo{
			Name:      e.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, backups)
}

func (s *server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	dir := s.backupsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Consistent database snapshot even while the server is busy.
	snap := filepath.Join(dir, ".snapshot.db")
	os.Remove(snap)
	if _, err := s.db.Exec(`VACUUM INTO ?`, snap); err != nil {
		writeError(w, http.StatusInternalServerError, "snapshotting database: "+err.Error())
		return
	}
	defer os.Remove(snap)

	name := "librinode-backup-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	zw := zip.NewWriter(f)
	add := func(entryName, src string) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := zw.Create(entryName)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		return err
	}
	err = add("librinode.db", snap)
	if err == nil {
		err = add("config.yaml", filepath.Join(s.cfg.DataDir(), "config.yaml"))
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		writeError(w, http.StatusInternalServerError, "writing backup: "+err.Error())
		return
	}

	info, _ := os.Stat(path)
	writeJSON(w, http.StatusCreated, backupInfo{
		Name: name, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
	})
}

func (s *server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := pathBackup(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	if err := os.Remove(filepath.Join(s.backupsDir(), name)); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := pathBackup(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	path := filepath.Join(s.backupsDir(), name)
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, path)
}

// handleRestoreBackup stages the backup's files as *.restore in the data
// directory; the swap happens on the next server start so the live database
// is never replaced under a running process.
func (s *server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := pathBackup(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	zr, err := zip.OpenReader(filepath.Join(s.backupsDir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "backup not found")
			return
		}
		writeError(w, http.StatusBadRequest, "opening backup: "+err.Error())
		return
	}
	defer zr.Close()

	staged := 0
	for _, entry := range zr.File {
		if entry.Name != "librinode.db" && entry.Name != "config.yaml" {
			continue
		}
		in, err := entry.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		dst := filepath.Join(s.cfg.DataDir(), entry.Name+".restore")
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_, err = io.Copy(out, in)
		in.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
			writeError(w, http.StatusInternalServerError, "staging "+entry.Name+": "+err.Error())
			return
		}
		staged++
	}
	if staged == 0 {
		writeError(w, http.StatusBadRequest, "backup contains no restorable files")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"staged":  staged,
		"message": "Restore staged — restart LibriNode to apply. The replaced files are kept as *.pre-restore.",
	})
}
