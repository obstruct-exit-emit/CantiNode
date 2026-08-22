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
func seedMusicFixture(t *testing.T, a *testAPI, rootFolderID int64, path string) (artistID, albumID, trackFileID int64) {
	t.Helper()
	artistID = seedMusicArtist(t, a, path)
	albumID, trackFileID = seedMusicAlbumFixture(t, a, artistID, rootFolderID, path)
	return artistID, albumID, trackFileID
}

// seedMusicArtist inserts a fresh artist row, keyed uniquely off path (as
// seedMusicFixture's own callers already do) so parallel/sequential test
// fixtures never collide.
func seedMusicArtist(t *testing.T, a *testAPI, path string) (artistID int64) {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES (?, ?, ?)`,
		"artist-"+path, "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedMusicAlbumFixture inserts an album/track/track_file under an
// already-existing artistID — the multi-album counterpart to
// seedMusicFixture, for tests that need two albums under the same artist.
func seedMusicAlbumFixture(t *testing.T, a *testAPI, artistID, rootFolderID int64, path string) (albumID, trackFileID int64) {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title) VALUES (?, ?, ?, ?)`,
		artistID, "album-"+path, "rg-"+path, "Geogaddi")
	if err != nil {
		t.Fatalf("insert album: %v", err)
	}
	albumID, _ = res.LastInsertId()

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
	return albumID, trackFileID
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

	artistID, _, _ := seedMusicFixture(t, a, rf.ID, trackPath)

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
	artistID, _, trackFileID := seedMusicFixture(t, a, rf.ID, trackPath)
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
	// Regression check for a real bug found live: a deleteFiles=true removal
	// used to unmatch the track_files row (the plain-delete behavior above)
	// instead of removing it — orphaning a row that pointed at a path
	// already gone from disk, left sitting in the Unmatched Files review
	// page until some later scan happened to notice and prune it.
	var stillThere int
	if err := a.db.QueryRow(`SELECT count(*) FROM track_files WHERE id = ?`, trackFileID).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere != 0 {
		t.Error("track_files row should be deleted outright, not left behind unmatched, when its file was deleted from disk")
	}
}

// TestDeleteAlbumWithFiles is TestDeleteArtistWithFiles' single-album
// counterpart — the album page's own "Remove album" action must delete
// files from disk exactly like the artist-wide one when asked, and must
// leave a sibling album's files (and the artist itself) completely alone.
func TestDeleteAlbumWithFiles(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	keptPath := filepath.Join(rootDir, "Boards of Canada", "Music Has the Right to Children", "01 - Wildlife Analysis.flac")
	trackPath := filepath.Join(rootDir, "Boards of Canada", "Geogaddi", "01 - Ready Lets Go.flac")
	for _, p := range []string{keptPath, trackPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	// Two albums on the same artist — deleting one must never touch the
	// other's file.
	artistID := seedMusicArtist(t, a, trackPath)
	seedMusicAlbumFixture(t, a, artistID, rf.ID, keptPath)
	albumID, trackFileID := seedMusicAlbumFixture(t, a, artistID, rf.ID, trackPath)

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/album/%d?deleteFiles=true", albumID), nil, nil), http.StatusOK)
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatal("album's file should be deleted from disk")
	}
	if _, err := os.Stat(filepath.Dir(trackPath)); !os.IsNotExist(err) {
		t.Fatal("emptied album directory should be pruned")
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("sibling album's file must survive: %v", err)
	}
	// Same regression check as TestDeleteArtistWithFiles: the row must be
	// deleted outright, not left behind unmatched, once its file is gone.
	var stillThere int
	if err := a.db.QueryRow(`SELECT count(*) FROM track_files WHERE id = ?`, trackFileID).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere != 0 {
		t.Error("track_files row should be deleted outright, not left behind unmatched, when its file was deleted from disk")
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

	_, _, trackFileID := seedMusicFixture(t, a, rf.ID, trackPath)

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/trackfile/%d", trackFileID), nil, nil), http.StatusNoContent)
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatal("track file should be deleted from disk")
	}
}

// seedInFlightGrab inserts a wanted_albums row at status='downloading' plus
// a matching grabs row at status='grabbed' — the state a real grab leaves
// behind while its download is still in progress. Returns both ids.
func seedInFlightGrab(t *testing.T, a *testAPI, artistID int64, releaseGroupMBID string) (wantedID, grabID int64) {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO wanted_albums (artist_id, release_group_mbid, title, status) VALUES (?, ?, ?, 'downloading')`,
		artistID, releaseGroupMBID, "In Flight")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	wantedID, _ = res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO grabs (wanted_album_id, title, protocol, status) VALUES (?, ?, 'torrent', 'grabbed')`,
		wantedID, "In Flight")
	if err != nil {
		t.Fatalf("seed grab: %v", err)
	}
	grabID, _ = res.LastInsertId()
	return wantedID, grabID
}

func grabStatus(t *testing.T, a *testAPI, grabID int64) (status, message string) {
	t.Helper()
	if err := a.db.QueryRow(`SELECT status, message FROM grabs WHERE id = ?`, grabID).Scan(&status, &message); err != nil {
		t.Fatalf("read grab status: %v", err)
	}
	return status, message
}

// TestRemoveArtistCancelsInFlightGrab is the regression test for a real
// edge case: removing an artist while one of its wanted albums is mid-
// download used to leave the grab pointing at a wanted_albums row that
// DeleteArtist's cascade had just deleted. internal/importer wouldn't
// crash over that (it already tolerates a missing wanted album on the
// success path), but the download would still finish and import — and
// musicscanner creates an artist from a matched file's own tags/MBID
// regardless of what CantiNode's own tables say, silently resurrecting the
// artist the user just removed. The fix cancels the grab first.
func TestRemoveArtistCancelsInFlightGrab(t *testing.T) {
	a := newTestAPI(t)
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES ('artist-inflight', 'In Flight Artist', 'In Flight Artist')`)
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	artistID, _ := res.LastInsertId()
	_, grabID := seedInFlightGrab(t, a, artistID, "rg-inflight")

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artistID), nil, nil), http.StatusOK)

	status, message := grabStatus(t, a, grabID)
	if status != "failed" || message != "artist removed" {
		t.Errorf("grab after artist removal: status=%q message=%q, want failed/\"artist removed\"", status, message)
	}
}

// TestRemoveAlbumCancelsOnlyThatAlbumsInFlightGrab mirrors the artist test
// at album scope, and confirms a sibling wanted album under the same
// artist is left completely alone.
func TestRemoveAlbumCancelsOnlyThatAlbumsInFlightGrab(t *testing.T) {
	a := newTestAPI(t)
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES ('artist-inflight2', 'In Flight Artist 2', 'In Flight Artist 2')`)
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	artistID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title) VALUES (?, 'album-inflight', 'rg-inflight-target', 'Target Album')`, artistID)
	if err != nil {
		t.Fatalf("seed album: %v", err)
	}
	albumID, _ := res.LastInsertId()

	_, targetGrabID := seedInFlightGrab(t, a, artistID, "rg-inflight-target")
	_, siblingGrabID := seedInFlightGrab(t, a, artistID, "rg-inflight-sibling")

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/album/%d", albumID), nil, nil), http.StatusOK)

	status, message := grabStatus(t, a, targetGrabID)
	if status != "failed" || message != "album removed" {
		t.Errorf("target grab after album removal: status=%q message=%q, want failed/\"album removed\"", status, message)
	}
	status, _ = grabStatus(t, a, siblingGrabID)
	if status != "grabbed" {
		t.Errorf("sibling grab after album removal: status=%q, want it left alone as \"grabbed\"", status)
	}
}
