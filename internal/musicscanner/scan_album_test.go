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
	track, err := s.db.GetOrCreateTrack(album.ID, "t-"+albumTitle, "Track One", 1, 1, 200000, "", "", "")
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

// TestScanAlbumFolderReapsAlbumLeftWithZeroFiles is the regression test
// for a real dead end: an album whose last file the prune above removes
// used to keep its now-empty albums row behind forever — invisible in
// Owned (needs a file), Missing (an albums row still exists), and Wanted
// (already converted away when first matched) all at once.
func TestScanAlbumFolderReapsAlbumLeftWithZeroFiles(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	album, path := seedAlbumWithFile(t, s, rf, "Boards of Canada", "Geogaddi", "Boards of Canada/Geogaddi")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ScanAlbumFolder(context.Background(), album.ID); err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}

	if _, err := s.db.GetAlbum(album.ID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetAlbum after its only file was pruned: err = %v, want ErrNotFound (reaped)", err)
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

	track, err := s.db.GetOrCreateTrack(album.ID, "t-phantom", "Track Two", 2, 1, 200000, "", "", "")
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

// TestScanAlbumFolderDiscoversNewFilesInSiblingDiscSubfolder is the
// regression test for a real gap: ScanAlbumFolder used to walk only
// filepath.Dir(existing[0].Path) — and ListTrackFilesByAlbum orders its
// result by path, so for a matched multi-disc album existing[0] is
// deterministically the CD1 file ("CD1" sorts before "CD2"). Any new file
// dropped into the CD2 sibling folder was therefore never discovered at
// all: no track_files row, no match attempt, nothing — WalkDir starting
// from CD1 has no way to reach a directory beside it. commonAncestorDir now
// walks from the shared parent of EVERY existing file's directory, which
// for this album is the "Moonglow (2CD)" folder covering both discs.
func TestScanAlbumFolderDiscoversNewFilesInSiblingDiscSubfolder(t *testing.T) {
	fs := newFolderTestServer()
	fs.releaseSearch = []mbReleaseSearchResult{
		{ID: "rel-main", Title: "Moonglow", Score: 100, TrackCount: 2,
			ArtistCredit: []mbArtistCredit{{Name: "Avantasia", Artist: mbArtistRef{ID: "artist-mbid", Name: "Avantasia"}}},
			ReleaseGroup: mbReleaseGroup{ID: "rg-main", Title: "Moonglow", PrimaryType: "Album"}},
	}
	fs.releaseLookups["rel-main"] = newTestAlbumRelease("rel-main", "Moonglow", "Track One", "Track Two")

	s, rf := newFolderTestScanner(t, fs)

	albumDir := filepath.Join(rf.Path, "Avantasia", "Moonglow (2CD)")
	cd1Dir := filepath.Join(albumDir, "CD1")
	cd2Dir := filepath.Join(albumDir, "CD2")
	if err := os.MkdirAll(cd1Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cd2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One track per disc — groupMultiDiscFolders merges CD1+CD2 into one
	// logical group for matching, exactly like a real 2-disc rip.
	buildFLACFile(t, cd1Dir, "01.flac", map[string]string{"ARTIST": "Avantasia", "ALBUM": "Moonglow", "TITLE": "Track One", "TRACKNUMBER": "1"})
	buildFLACFile(t, cd2Dir, "01.flac", map[string]string{"ARTIST": "Avantasia", "ALBUM": "Moonglow", "TITLE": "Track Two", "TRACKNUMBER": "1"})

	result, err := s.ScanRootFolder(t.Context(), rf)
	if err != nil {
		t.Fatalf("initial ScanRootFolder: %v", err)
	}
	if result.FilesMatched != 2 {
		t.Fatalf("initial scan FilesMatched = %d, want 2 (result=%+v)", result.FilesMatched, result)
	}

	albums, err := s.db.ListAlbumsByArtist(mustArtistID(t, s, "artist-mbid"))
	if err != nil || len(albums) != 1 {
		t.Fatalf("albums = %+v, err %v, want exactly 1", albums, err)
	}
	album := albums[0]

	existing, err := s.db.ListTrackFilesByAlbum(album.ID)
	if err != nil || len(existing) != 2 {
		t.Fatalf("existing = %+v, err %v, want 2 matched files", existing, err)
	}
	if filepath.Dir(existing[0].Path) != cd1Dir {
		t.Fatalf("existing[0] = %q, want the CD1 file (path-ordering assumption this test depends on)", existing[0].Path)
	}

	// A bonus track arrives later, dropped directly into CD2 — the disc
	// existing[0] (CD1) knows nothing about. Whether this new lone file
	// goes on to match anything isn't the point (it's alone in its group,
	// takes the per-file fuzzy fallback with no recording-search fixture
	// configured, and will simply stay unmatched) — what matters is that
	// the walk reaches it at all and gives it a track_files row.
	buildFLACFile(t, cd2Dir, "02.flac", map[string]string{"ARTIST": "Avantasia", "ALBUM": "Moonglow", "TITLE": "Bonus Track", "TRACKNUMBER": "2"})

	if _, err := s.ScanAlbumFolder(t.Context(), album.ID); err != nil {
		t.Fatalf("ScanAlbumFolder: %v", err)
	}

	files, err := s.db.ListTrackFilesByRootFolder(rf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("track files under root folder = %+v, want 3 (both original disc files plus the newly-discovered CD2 bonus track)", files)
	}
}
