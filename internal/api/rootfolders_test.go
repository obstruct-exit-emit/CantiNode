package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func addRootFolder(t *testing.T, a *testAPI, path, name string) rootFolder {
	t.Helper()
	var f rootFolder
	a.want(a.call("POST", "/api/v1/rootfolder", map[string]any{
		"mediaType": "music", "path": path, "name": name,
	}, &f), http.StatusCreated)
	return f
}

func TestAddRootFolderAutoNamesFromPathAndBecomesDefault(t *testing.T) {
	a := newTestAPI(t)
	dir := t.TempDir()
	f := addRootFolder(t, a, dir, "")
	if f.Name != filepath.Base(dir) {
		t.Errorf("Name = %q, want %q (auto-named from path)", f.Name, filepath.Base(dir))
	}
	if !f.IsDefault {
		t.Error("the first root folder ever added should become the default automatically")
	}
}

func TestAddSecondRootFolderDoesNotBecomeDefault(t *testing.T) {
	a := newTestAPI(t)
	addRootFolder(t, a, t.TempDir(), "First")
	second := addRootFolder(t, a, t.TempDir(), "Second")
	if second.IsDefault {
		t.Error("a second root folder should not automatically become the default")
	}
}

func TestRenameRootFolder(t *testing.T) {
	a := newTestAPI(t)
	f := addRootFolder(t, a, t.TempDir(), "Original")

	a.want(a.call("PUT", "/api/v1/rootfolder/"+itoa(f.ID)+"/name", map[string]string{"name": "Renamed"}, nil), http.StatusNoContent)

	var folders []rootFolder
	a.want(a.call("GET", "/api/v1/rootfolder", nil, &folders), http.StatusOK)
	if len(folders) != 1 || folders[0].Name != "Renamed" {
		t.Errorf("folders = %+v, want one named Renamed", folders)
	}
}

func TestRenameRootFolderNotFound(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("PUT", "/api/v1/rootfolder/999/name", map[string]string{"name": "X"}, nil), http.StatusNotFound)
}

func TestSetDefaultRootFolder(t *testing.T) {
	a := newTestAPI(t)
	first := addRootFolder(t, a, t.TempDir(), "First")
	second := addRootFolder(t, a, t.TempDir(), "Second")

	a.want(a.call("PUT", "/api/v1/rootfolder/"+itoa(second.ID)+"/default", nil, nil), http.StatusNoContent)

	var folders []rootFolder
	a.want(a.call("GET", "/api/v1/rootfolder", nil, &folders), http.StatusOK)
	byID := map[int64]rootFolder{}
	for _, f := range folders {
		byID[f.ID] = f
	}
	if byID[first.ID].IsDefault {
		t.Error("first root folder should no longer be default")
	}
	if !byID[second.ID].IsDefault {
		t.Error("second root folder should now be default")
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// seedMovableArtist creates an artist with one real, matched track file
// on disk under rootFolderID — the minimum a move test needs, mirroring
// internal/musicscanner's own mover_test.go seeding but reachable through
// the HTTP API's db handle.
func seedMovableArtist(t *testing.T, a *testAPI, rootFolderID int64, rootPath, uniq string) (artistID, trackFileID int64) {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES (?, ?, ?)`, "artist-"+uniq, "Artist "+uniq, "Artist "+uniq)
	if err != nil {
		t.Fatal(err)
	}
	artistID, _ = res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title) VALUES (?, ?, ?, ?)`,
		artistID, "album-"+uniq, "rg-"+uniq, "Album")
	if err != nil {
		t.Fatal(err)
	}
	albumID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO tracks (album_id, mbid, title, track_number) VALUES (?, ?, ?, ?)`,
		albumID, "track-"+uniq, "Track", 1)
	if err != nil {
		t.Fatal(err)
	}
	trackID, _ := res.LastInsertId()

	relPath := filepath.Join("Artist "+uniq, "01.flac")
	fullPath := filepath.Join(rootPath, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("audio data "+uniq), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err = a.db.Exec(`INSERT INTO track_files (root_folder_id, track_id, path, size_bytes, format, match_status) VALUES (?, ?, ?, ?, ?, ?)`,
		rootFolderID, trackID, fullPath, 12, "flac", "matched")
	if err != nil {
		t.Fatal(err)
	}
	trackFileID, _ = res.LastInsertId()
	return artistID, trackFileID
}

func TestPreviewMoveMusicArtist(t *testing.T) {
	a := newTestAPI(t)
	srcDir, destDir := t.TempDir(), t.TempDir()
	src := addRootFolder(t, a, srcDir, "Source")
	dest := addRootFolder(t, a, destDir, "Destination")
	artistID, _ := seedMovableArtist(t, a, src.ID, srcDir, "p1")

	var result struct {
		Moves []struct {
			FileID    int64  `json:"fileId"`
			From      string `json:"from"`
			To        string `json:"to"`
			SizeBytes int64  `json:"sizeBytes"`
		} `json:"moves"`
		TotalBytes int64 `json:"totalBytes"`
	}
	a.want(a.call("GET", "/api/v1/music/artist/"+itoa(artistID)+"/move/preview?rootFolderId="+itoa(dest.ID), nil, &result), http.StatusOK)

	if len(result.Moves) != 1 {
		t.Fatalf("moves = %+v, want 1", result.Moves)
	}
	if result.TotalBytes != 12 {
		t.Errorf("totalBytes = %d, want 12", result.TotalBytes)
	}
}

func TestMoveMusicArtistRunsInBackgroundAndUpdatesFiles(t *testing.T) {
	a := newTestAPI(t)
	srcDir, destDir := t.TempDir(), t.TempDir()
	src := addRootFolder(t, a, srcDir, "Source")
	dest := addRootFolder(t, a, destDir, "Destination")
	artistID, tfID := seedMovableArtist(t, a, src.ID, srcDir, "p2")

	a.want(a.call("POST", "/api/v1/music/artist/"+itoa(artistID)+"/move", map[string]any{"rootFolderId": dest.ID}, nil), http.StatusAccepted)

	var state musicMoveState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.want(a.call("GET", "/api/v1/music/move/status", nil, &state), http.StatusOK)
		if !state.Running && state.FinishedAt != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state.Running {
		t.Fatal("move did not finish within the test deadline")
	}
	if len(state.Errors) != 0 {
		t.Fatalf("move errors = %v, want none", state.Errors)
	}
	if len(state.Moved) != 1 {
		t.Fatalf("moved = %+v, want 1", state.Moved)
	}

	var path string
	var rootFolderID int64
	if err := a.db.QueryRow(`SELECT path, root_folder_id FROM track_files WHERE id = ?`, tfID).Scan(&path, &rootFolderID); err != nil {
		t.Fatal(err)
	}
	if rootFolderID != dest.ID {
		t.Errorf("root_folder_id = %d, want %d", rootFolderID, dest.ID)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("moved file should exist at %s: %v", path, err)
	}
}

