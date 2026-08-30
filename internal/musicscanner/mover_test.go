package musicscanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagwriter"
)

// setupMoveScanner builds a Scanner (no MusicBrainz client needed — the
// move feature never calls one) with two named root folders, srcDir and
// destDir, both real temp directories on disk.
func setupMoveScanner(t *testing.T) (s *Scanner, db *musiclibrary.Store, srcRoot, destRoot musiclibrary.RootFolder) {
	t.Helper()
	sqlDB, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db = musiclibrary.NewStore(sqlDB)

	mk := func(name, dir string) musiclibrary.RootFolder {
		res, err := sqlDB.Exec(`INSERT INTO root_folders (media_type, path, name) VALUES ('music', ?, ?)`, dir, name)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		rf, err := db.GetRootFolder(id)
		if err != nil {
			t.Fatal(err)
		}
		return *rf
	}
	srcRoot = mk("Source", t.TempDir())
	destRoot = mk("Destination", t.TempDir())

	s = New(db, nil, nil, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false, tagwriter.AllEnabled, false)
	return s, db, srcRoot, destRoot
}

// seedMoveFile creates an artist (if artistID is 0)/album/track/matched
// track file at relPath under root.Path, with real bytes on disk, and
// returns the artist id and the track file's own id.
func seedMoveFile(t *testing.T, db *musiclibrary.Store, artistID int64, root musiclibrary.RootFolder, relPath, uniq string, content []byte) (newArtistID, trackFileID int64) {
	t.Helper()
	if artistID == 0 {
		artist, err := db.GetOrCreateArtist("a-"+uniq, "Artist "+uniq, "Artist "+uniq)
		if err != nil {
			t.Fatal(err)
		}
		artistID = artist.ID
	}
	album, err := db.GetOrCreateAlbum(artistID, "al-"+uniq, "rg-"+uniq, "Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := db.GetOrCreateTrack(album.ID, "t-"+uniq, "Track "+uniq, 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root.Path, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	tf, err := db.UpsertTrackFileByPath(root.ID, path, int64(len(content)), "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
	return artistID, tf.ID
}

func TestPlanMoveArtistPreservesRelativePath(t *testing.T) {
	s, db, srcRoot, destRoot := setupMoveScanner(t)
	artistID, _ := seedMoveFile(t, db, 0, srcRoot, "Artist X/Album/01 - Track.flac", "x1", []byte("data"))

	moves, err := s.PlanMoveArtist(artistID, destRoot.ID)
	if err != nil {
		t.Fatalf("PlanMoveArtist: %v", err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %+v, want 1", moves)
	}
	wantTo := filepath.Join(destRoot.Path, "Artist X/Album/01 - Track.flac")
	if moves[0].To != wantTo {
		t.Errorf("To = %q, want %q (same relative path, different root)", moves[0].To, wantTo)
	}
	if moves[0].SizeBytes != 4 {
		t.Errorf("SizeBytes = %d, want 4", moves[0].SizeBytes)
	}
}

func TestPlanMoveArtistSkipsFilesAlreadyOnDestination(t *testing.T) {
	s, db, srcRoot, destRoot := setupMoveScanner(t)
	artistID, _ := seedMoveFile(t, db, 0, srcRoot, "a.flac", "y1", []byte("d1"))
	seedMoveFile(t, db, artistID, destRoot, "b.flac", "y2", []byte("d2"))

	moves, err := s.PlanMoveArtist(artistID, destRoot.ID)
	if err != nil {
		t.Fatalf("PlanMoveArtist: %v", err)
	}
	if len(moves) != 1 || filepath.Base(moves[0].From) != "a.flac" {
		t.Fatalf("moves = %+v, want only the file not already on destRoot", moves)
	}
}

func TestMoveArtistCopiesUpdatesDBAndRemovesOriginal(t *testing.T) {
	s, db, srcRoot, destRoot := setupMoveScanner(t)
	artistID, tfID := seedMoveFile(t, db, 0, srcRoot, "Artist/Album/01.flac", "z1", []byte("hello world"))
	oldPath := filepath.Join(srcRoot.Path, "Artist/Album/01.flac")

	moved, errs, err := s.MoveArtist(context.Background(), artistID, destRoot.ID)
	if err != nil {
		t.Fatalf("MoveArtist: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if len(moved) != 1 {
		t.Fatalf("moved = %+v, want 1", moved)
	}

	newPath := filepath.Join(destRoot.Path, "Artist/Album/01.flac")
	content, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("new file content = %q, want %q", content, "hello world")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old file should be gone, stat err = %v", err)
	}
	// The now-empty Artist/Album and Artist directories under srcRoot
	// should have been cleaned up too.
	if _, err := os.Stat(filepath.Dir(oldPath)); !os.IsNotExist(err) {
		t.Errorf("empty leftover album directory should have been removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(oldPath))); !os.IsNotExist(err) {
		t.Errorf("empty leftover artist directory should have been removed, stat err = %v", err)
	}

	tf, err := db.GetTrackFile(tfID)
	if err != nil {
		t.Fatal(err)
	}
	if tf.RootFolderID != destRoot.ID {
		t.Errorf("RootFolderID = %d, want %d", tf.RootFolderID, destRoot.ID)
	}
	if tf.Path != newPath {
		t.Errorf("Path = %q, want %q", tf.Path, newPath)
	}
}

// TestMoveArtistRefusesDestinationCollisionAndContinuesPastIt is the
// multi-file resilience test: one file's destination is already occupied
// by an unrelated file (refused, original left untouched), the other
// file's move must still succeed — mirroring applyOrganizePlan's own
// "one bad file never stops the rest" convention.
func TestMoveArtistRefusesDestinationCollisionAndContinuesPastIt(t *testing.T) {
	s, db, srcRoot, destRoot := setupMoveScanner(t)
	artistID, _ := seedMoveFile(t, db, 0, srcRoot, "collide.flac", "c1", []byte("mine"))
	_, tfID2 := seedMoveFile(t, db, artistID, srcRoot, "fine.flac", "c2", []byte("also mine"))

	// Something else already occupies the first file's planned destination.
	collidePath := filepath.Join(destRoot.Path, "collide.flac")
	if err := os.MkdirAll(filepath.Dir(collidePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collidePath, []byte("not mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	moved, errs, err := s.MoveArtist(context.Background(), artistID, destRoot.ID)
	if err != nil {
		t.Fatalf("MoveArtist: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (the collision)", errs)
	}
	if len(moved) != 1 || filepath.Base(moved[0].From) != "fine.flac" {
		t.Fatalf("moved = %+v, want just fine.flac", moved)
	}

	// The collided file's original must survive untouched, and the
	// unrelated file already at the destination must survive too.
	origPath := filepath.Join(srcRoot.Path, "collide.flac")
	if content, err := os.ReadFile(origPath); err != nil || string(content) != "mine" {
		t.Errorf("original collide.flac = %q, %v, want survived with its own content", content, err)
	}
	if content, err := os.ReadFile(collidePath); err != nil || string(content) != "not mine" {
		t.Errorf("destination collide.flac = %q, %v, want untouched", content, err)
	}

	tf2, err := db.GetTrackFile(tfID2)
	if err != nil {
		t.Fatal(err)
	}
	if tf2.RootFolderID != destRoot.ID {
		t.Errorf("fine.flac RootFolderID = %d, want moved to %d", tf2.RootFolderID, destRoot.ID)
	}
}

// TestMoveTrackFileRestoresOriginalOnDBFailure is the regression test for
// a real bug found in review: if the copy+rename to the new location
// succeeded but the database update (SetTrackFileLocation) then failed,
// the file used to be left stranded at the new path with the database
// still pointing at the old one — every retry would then hit this
// function's own very first check ("destination already exists") forever,
// since nothing about the stored state ever changed: a permanently stuck
// file. moveTrackFile now moves the file back to its original location
// when the database update fails, so a retry starts clean.
//
// The DB failure is triggered realistically rather than mocked: a second
// track_files row is seeded directly at the exact path this move will try
// to claim (no real file there, so moveTrackFile's own disk-collision
// check passes) — the eventual SetTrackFileLocation UPDATE then hits
// track_files.path's UNIQUE constraint and fails, exactly like any other
// DB-level failure at that step would.
func TestMoveTrackFileRestoresOriginalOnDBFailure(t *testing.T) {
	s, db, srcRoot, destRoot := setupMoveScanner(t)

	collisionPath := filepath.Join(destRoot.Path, "Artist Y/Album/01 - Track.flac")
	if _, err := db.UpsertTrackFileByPath(destRoot.ID, collisionPath, 1, "flac", 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}

	_, trackFileID := seedMoveFile(t, db, 0, srcRoot, "Artist Y/Album/01 - Track.flac", "y1", []byte("real audio data"))
	tfBefore, err := db.GetTrackFile(trackFileID)
	if err != nil {
		t.Fatal(err)
	}
	origPath := tfBefore.Path

	if err := s.moveTrackFile(trackFileID, destRoot.ID, collisionPath); err == nil {
		t.Fatal("want an error from the path collision in the database, got nil")
	}

	if _, statErr := os.Stat(origPath); statErr != nil {
		t.Errorf("original file %s missing after rollback: %v", origPath, statErr)
	}
	if _, statErr := os.Stat(collisionPath); statErr == nil {
		t.Errorf("destination %s still has the file after rollback — should have been moved back", collisionPath)
	}

	tfAfter, err := db.GetTrackFile(trackFileID)
	if err != nil {
		t.Fatal(err)
	}
	if tfAfter.Path != origPath || tfAfter.RootFolderID != srcRoot.ID {
		t.Errorf("track file after failed move = %+v, want unchanged (path=%s, root=%d)", tfAfter, origPath, srcRoot.ID)
	}
}

func TestRemoveEmptyParentsStopsAtBoundaryAndNonEmptyDir(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// A sibling file under "a" keeps that level non-empty once "b" is
	// cleaned up — cleanup must stop there, not remove "a" or base.
	if err := os.WriteFile(filepath.Join(base, "a", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	removeEmptyParents(nested, base)

	if _, err := os.Stat(filepath.Join(base, "a", "b")); !os.IsNotExist(err) {
		t.Errorf("empty dir 'b' should have been removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "a")); err != nil {
		t.Errorf("'a' should survive (still has keep.txt), stat err = %v", err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Errorf("boundary itself should never be removed, stat err = %v", err)
	}
}
