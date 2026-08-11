package musicscanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// seedAlbumWithFile creates an artist/album/track and a matched track_file
// row backed by a real file on disk under rf, returning the album and the
// file's own path. No MusicBrainz client is needed — ScanAlbumFolder's
// pruning path (this file's whole point) never calls one, only the
// already-existing test setup helper (setupOrganizeScanner) does the same.
func seedAlbumWithFile(t *testing.T, s *Scanner, rf musiclibrary.RootFolder, artistName, albumTitle, subdir string) (album *musiclibrary.Album, path string) {
	t.Helper()
	artist, err := s.db.GetOrCreateArtist("a-"+albumTitle, artistName, artistName)
	if err != nil {
		t.Fatal(err)
	}
	album, err = s.db.GetOrCreateAlbum(artist.ID, "al-"+albumTitle, "rg-"+albumTitle, albumTitle, "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(album.ID, "t-"+albumTitle, "Track One", 1, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(rf.Path, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "01 - Track One.flac")
	if err := os.WriteFile(path, []byte("fake audio data"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
	return album, path
}

// TestScanAlbumFolderPrunesFileDeletedOutsideApp is the regression test for
// a real gap: unlike a full ScanRootFolder pass, ScanAlbumFolder never ran
// DeleteTrackFilesMissing (root-folder-wide, unsafe to run scoped to one
// album's directory) — meaning a file removed from disk by hand used to
// leave its track_files row behind forever, undetected, with "Scan files"
// silently reporting nothing wrong.
func TestScanAlbumFolderPrunesFileDeletedOutsideApp(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	album, path := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanAlbumFolder(context.Background(), album.ID)
	if err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}
	if result.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want 1", result.FilesRemoved)
	}

	files, err := s.db.ListTrackFilesByAlbum(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("track files after scan = %+v, want the stale row pruned", files)
	}
}

// TestScanAlbumFolderPrunesWhenWholeFolderDeleted covers the more extreme
// case — not just the file but its entire containing directory is gone —
// which fails filepath.WalkDir's very first step rather than simply
// omitting one file from the walk. The pruning pass must still run.
func TestScanAlbumFolderPrunesWhenWholeFolderDeleted(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	album, path := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")

	if err := os.RemoveAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanAlbumFolder(context.Background(), album.ID)
	if err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}
	if result.FilesRemoved != 1 {
		t.Errorf("FilesRemoved = %d, want 1", result.FilesRemoved)
	}
	if len(result.Errors) == 0 {
		t.Error("want the walk failure (missing directory) surfaced in Errors")
	}

	files, err := s.db.ListTrackFilesByAlbum(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("track files after scan = %+v, want the stale row pruned", files)
	}
}

// TestScanAlbumFolderKeepsFileOnNonNotExistStatError is the regression test
// for a real bug: the prune loop used to treat *any* non-nil os.Stat error
// as "the file is gone," not specifically "not found" — a transient
// permission error, a briefly-disconnected network mount, or (as
// exercised here, portably and without relying on permission bits a test
// runner might have privileges to bypass) a path segment that briefly
// isn't a directory would all read as confirmed deletion and silently
// drop the track_files row for a file nothing actually removed.
func TestScanAlbumFolderKeepsFileOnNonNotExistStatError(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	album, path := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")
	dir := filepath.Dir(path)

	// "blocker" is a regular file, not a directory — stat'ing anything
	// nested under it fails with ENOTDIR, which os.IsNotExist reports as
	// false (unlike ENOENT), regardless of who's running the test.
	blockerPath := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blockerPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	phantomPath := filepath.Join(blockerPath, "02 - Track Two.flac")

	track, err := s.db.GetOrCreateTrack(album.ID, "t-phantom", "Track Two", 2, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, phantomPath, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	result, err := s.ScanAlbumFolder(context.Background(), album.ID)
	if err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}
	if result.FilesRemoved != 0 {
		t.Errorf("FilesRemoved = %d, want 0 — nothing was confirmed deleted", result.FilesRemoved)
	}
	if len(result.Errors) == 0 {
		t.Error("want the stat failure surfaced in Errors instead of silently pruning")
	}

	files, err := s.db.ListTrackFilesByAlbum(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("track files after scan = %+v, want both rows to survive (the real file and the phantom one)", files)
	}
}

// TestScanAlbumFolderNeverTouchesSiblingAlbum confirms the pruning pass
// stays scoped to exactly the album being scanned — a sibling album under
// the same artist (and same root folder) must survive untouched even
// though ScanAlbumFolder has no DeleteTrackFilesMissing-style whole-root
// context to lean on.
func TestScanAlbumFolderNeverTouchesSiblingAlbum(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	target, targetPath := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")
	sibling, siblingPath := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Tomorrow's Harvest", "Boards of Canada/Tomorrows Harvest")

	if err := os.Remove(targetPath); err != nil {
		t.Fatal(err)
	}
	// The sibling's file is left in place; only its row would be at risk if
	// scoping were broken.

	if _, err := s.ScanAlbumFolder(context.Background(), target.ID); err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}

	files, err := s.db.ListTrackFilesByAlbum(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("target album track files = %+v, want the stale row pruned", files)
	}

	siblingFiles, err := s.db.ListTrackFilesByAlbum(sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(siblingFiles) != 1 || siblingFiles[0].Path != siblingPath {
		t.Errorf("sibling album track files = %+v, want its one file untouched", siblingFiles)
	}
}
