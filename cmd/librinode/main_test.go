package main

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/database"
)

// writeBackupZip builds a backup archive the same shape handleCreateBackup
// produces: the database snapshot as librinode.db, config.yaml alongside it.
func writeBackupZip(t *testing.T, dest, dbSnapshot, configPath string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create backup zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	add := func(entryName, src string) {
		in, err := os.Open(src)
		if err != nil {
			t.Fatalf("open %s: %v", src, err)
		}
		defer in.Close()
		out, err := zw.Create(entryName)
		if err != nil {
			t.Fatalf("zip entry %s: %v", entryName, err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatalf("copy %s: %v", entryName, err)
		}
	}
	add("librinode.db", dbSnapshot)
	add("config.yaml", configPath)
	if err := zw.Close(); err != nil {
		t.Fatalf("close backup zip: %v", err)
	}
}

// stageBackup extracts a backup's restorable files into dataDir as *.restore,
// mirroring handleRestoreBackup — the on-disk state a user reaches by placing a
// backup on a fresh install and hitting Restore, just before the restart.
func stageBackup(t *testing.T, backupZip, dataDir string) {
	t.Helper()
	zr, err := zip.OpenReader(backupZip)
	if err != nil {
		t.Fatalf("open backup zip: %v", err)
	}
	defer zr.Close()
	for _, entry := range zr.File {
		if entry.Name != "librinode.db" && entry.Name != "config.yaml" {
			continue
		}
		in, err := entry.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", entry.Name, err)
		}
		dst := filepath.Join(dataDir, entry.Name+".restore")
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			t.Fatalf("stage %s: %v", dst, err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatalf("copy %s: %v", entry.Name, err)
		}
		in.Close()
		out.Close()
	}
}

// TestCleanMachineRestore is the clean-machine half of the backup/restore
// drill: a backup taken on a populated data dir is applied to a brand-new,
// empty one (as a fresh install would), and the library must come back whole —
// database rows intact, config in place, and — because a clean machine has no
// live files to protect — nothing left behind as *.restore or *.pre-restore.
func TestCleanMachineRestore(t *testing.T) {
	// --- Source machine: a populated data dir, then a backup of it. ---
	src := t.TempDir()
	db, err := database.Open(filepath.Join(src, "librinode.db"))
	if err != nil {
		t.Fatalf("open source db: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO authors (metadata_source, foreign_id, name, sort_name) VALUES ('hardcover', 'hc-1', 'Ursula K. Le Guin', 'Le Guin, Ursula K.')`,
	); err != nil {
		t.Fatalf("seed author: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO books (author_id, foreign_id, title, sort_title) VALUES (1, 'hc-b1', 'A Wizard of Earthsea', 'wizard of earthsea a')`,
	); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	configPath := filepath.Join(src, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 7845\napi_key: test-key-123\n"), 0o644); err != nil {
		t.Fatalf("write source config: %v", err)
	}

	// A consistent snapshot (what handleCreateBackup zips), then the archive.
	snapshot := filepath.Join(src, "snapshot.db")
	if _, err := db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	db.Close()
	backupZip := filepath.Join(src, "librinode-backup-20260718-000000.zip")
	writeBackupZip(t, backupZip, snapshot, configPath)

	// --- Clean machine: a fresh, empty data dir. ---
	clean := t.TempDir()
	stageBackup(t, backupZip, clean)

	// The swap that runs at startup.
	if err := applyPendingRestore(clean); err != nil {
		t.Fatalf("applyPendingRestore: %v", err)
	}

	// A clean machine had no live files, so nothing should have been preserved
	// as *.pre-restore, and the staged files must have been consumed.
	for _, leftover := range []string{
		"librinode.db.pre-restore", "config.yaml.pre-restore",
		"librinode.db.restore", "config.yaml.restore",
	} {
		if _, err := os.Stat(filepath.Join(clean, leftover)); err == nil {
			t.Errorf("unexpected leftover after clean-machine restore: %s", leftover)
		}
	}

	// config.yaml came back verbatim.
	gotConfig, err := os.ReadFile(filepath.Join(clean, "config.yaml"))
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if !strings.Contains(string(gotConfig), "test-key-123") {
		t.Errorf("restored config.yaml lost its contents: %q", gotConfig)
	}

	// The restored database opens and the seeded library is whole.
	restored, err := database.Open(filepath.Join(clean, "librinode.db"))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restored.Close()

	var name string
	if err := restored.QueryRow(`SELECT name FROM authors WHERE foreign_id = 'hc-1'`).Scan(&name); err != nil {
		t.Fatalf("restored author lookup: %v", err)
	}
	if name != "Ursula K. Le Guin" {
		t.Errorf("restored author name = %q, want Ursula K. Le Guin", name)
	}
	var books int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&books); err != nil {
		t.Fatalf("restored book count: %v", err)
	}
	if books != 1 {
		t.Errorf("restored book count = %d, want 1", books)
	}
}
