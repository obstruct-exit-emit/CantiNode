package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// seedMusicFixture inserts a minimal artist/album/track/track_file directly
// (bypassing scan/monitor, both of which would otherwise reach the real
// MusicBrainz API) — enough to exercise the delete-with-files paths that
// share internal/api's generic removeFilesFromDisk/finishDelete helpers.
func seedMusicFixture(t *testing.T, a *testAPI, rootFolderID int64, path string) (artistID, trackFileID int64) {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES (?, ?, ?)`,
		"artist-"+path, "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	artistID, _ = res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title) VALUES (?, ?, ?, ?)`,
		artistID, "album-"+path, "rg-"+path, "Geogaddi")
	if err != nil {
		t.Fatalf("insert album: %v", err)
	}
	albumID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO tracks (album_id, mbid, title, track_number) VALUES (?, ?, ?, ?)`,
		albumID, "track-"+path, "Ready Lets Go", 1)
	if err != nil {
		t.Fatalf("insert track: %v", err)
	}
	trackID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO track_files (root_folder_id, track_id, path, format, match_status) VALUES (?, ?, ?, ?, ?)`,
		rootFolderID, trackID, path, "flac", "matched")
	if err != nil {
		t.Fatalf("insert track file: %v", err)
	}
	trackFileID, _ = res.LastInsertId()
	return artistID, trackFileID
}

func TestDeleteArtistWithFiles(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	trackPath := filepath.Join(rootDir, "Boards of Canada", "Geogaddi", "01 - Ready Lets Go.flac")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	artistID, _ := seedMusicFixture(t, a, rf.ID, trackPath)

	// Default delete is DB-only: the file survives on disk (a later scan
	// re-finds it as a stray).
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artistID), nil, nil), http.StatusOK)
	if _, err := os.Stat(trackPath); err != nil {
		t.Fatalf("file should survive a plain delete: %v", err)
	}

	// The plain delete above unmatches (not deletes) the track_file row —
	// it survives, path and all, as an unmatched stray. Clear it before
	// re-seeding under the same path.
	if _, err := a.db.Exec(`DELETE FROM track_files WHERE path = ?`, trackPath); err != nil {
		t.Fatalf("clearing stray track_file: %v", err)
	}

	// Re-seed, then delete WITH files.
	artistID, _ = seedMusicFixture(t, a, rf.ID, trackPath)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d?deleteFiles=true", artistID), nil, nil), http.StatusOK)
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatal("file should be deleted from disk")
	}
	if _, err := os.Stat(filepath.Dir(trackPath)); !os.IsNotExist(err) {
		t.Fatal("emptied album directory should be pruned")
	}
	if _, err := os.Stat(rootDir); err != nil {
		t.Fatalf("root folder itself must survive: %v", err)
	}
}

func TestDeleteTrackFileRemovesFromDisk(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	trackPath := filepath.Join(rootDir, "Boards of Canada", "Geogaddi", "01 - Ready Lets Go.flac")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	_, trackFileID := seedMusicFixture(t, a, rf.ID, trackPath)

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/trackfile/%d", trackFileID), nil, nil), http.StatusNoContent)
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatal("track file should be deleted from disk")
	}
}
