package musicscanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestScanFolderOnlyTouchesGivenDirectory is the regression test for a real
// performance bug: internal/importer used to run a full ScanAll after every
// completed grab, re-stat-ing and re-querying every already-matched file in
// the entire library — not just the handful of genuinely new ones — on
// every single import. Confirmed live against a real 944-file library
// (likely network-mounted storage, ~190ms/file): a multi-minute tax per
// import, completely unrelated to how much was actually being imported.
// ScanFolder must never discover, upsert, or otherwise touch a file that
// sits outside the directory it was given, no matter how large the rest of
// the library under the same root folder is.
func TestScanFolderOnlyTouchesGivenDirectory(t *testing.T) {
	s, rf := setupOrganizeScanner(t)

	elsewhereDir := filepath.Join(rf.Path, "Elsewhere Artist", "Elsewhere Album")
	if err := os.MkdirAll(elsewhereDir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewherePath := filepath.Join(elsewhereDir, "01 - Song.flac")
	if err := os.WriteFile(elsewherePath, []byte("fake audio data"), 0o644); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(rf.Path, "New Artist", "New Album")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(newDir, "01 - New Song.flac")
	if err := os.WriteFile(newPath, []byte("fake audio data"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanFolder(context.Background(), rf, newDir)
	if err != nil {
		t.Fatalf("ScanFolder: %v", err)
	}
	// Scoping is what's under test here, not tag-reading — the fixture
	// content isn't real audio, so FilesFound (incremented before the tag
	// read is even attempted) is what proves the walk visited exactly the
	// one file under newDir and nothing else.
	if result.FilesFound != 1 {
		t.Errorf("FilesFound = %d, want 1 (only the file under the scanned directory)", result.FilesFound)
	}
	if _, err := s.db.GetTrackFileByPath(elsewherePath); err == nil {
		t.Error("ScanFolder touched a file outside the directory it was given — it must leave the rest of the library completely untouched")
	}
}
