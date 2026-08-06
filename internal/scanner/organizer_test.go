package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
)

func TestSanitizePathComponent(t *testing.T) {
	got := sanitizePathComponent(`AC/DC: "Greatest" <Hits>?`)
	for _, bad := range illegalPathChars {
		if got != "" && containsRune(got, bad) {
			t.Errorf("sanitizePathComponent result %q still contains illegal char %q", got, string(bad))
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

func TestFormatPath(t *testing.T) {
	artist := database.Artist{Name: "Boards of Canada"}
	album := database.Album{Title: "Geogaddi", ReleaseDate: "2002-02-04"}
	track := database.Track{Title: "Alpha and Omega", TrackNumber: 3, DiscNumber: 1}

	got := FormatPath("{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}", artist, album, track, ".flac")
	want := filepath.FromSlash("Boards of Canada/Geogaddi (2002)/03 - Alpha and Omega.flac")
	if got != want {
		t.Errorf("FormatPath = %q, want %q", got, want)
	}
}

func TestFormatPathSanitizesIllegalCharacters(t *testing.T) {
	artist := database.Artist{Name: `AC/DC`}
	album := database.Album{Title: "Greatest Hits", ReleaseDate: "1990"}
	track := database.Track{Title: `Track: "One"`, TrackNumber: 1, DiscNumber: 1}

	got := FormatPath("{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", artist, album, track, ".mp3")
	if containsRune(filepath.Base(got), ':') || containsRune(filepath.Base(got), '"') {
		t.Errorf("FormatPath result %q still has illegal filename characters", got)
	}
}

// setupOrganizeScanner returns a Scanner (no MusicBrainz client needed —
// OrganizeFile never calls it), a root folder row, and its on-disk path.
func setupOrganizeScanner(t *testing.T) (*Scanner, database.RootFolder) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	rootDir := t.TempDir()
	rf, err := db.CreateRootFolder(t.Context(), rootDir)
	if err != nil {
		t.Fatal(err)
	}

	s := New(db, nil, nil, "{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}", 0.75, false)
	return s, *rf
}

func TestOrganizeFileMovesAndRecordsPath(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	artist, err := s.db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := s.db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := s.db.GetOrCreateTrack(ctx, album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}

	srcPath := filepath.Join(rf.Path, "unsorted.flac")
	if err := os.WriteFile(srcPath, []byte("fake audio data"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, srcPath, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tf.ID, &track.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	newPath, err := s.OrganizeFile(ctx, tf.ID)
	if err != nil {
		t.Fatalf("OrganizeFile: %v", err)
	}

	wantSuffix := filepath.FromSlash("Boards of Canada/Geogaddi (2002)/03 - Alpha and Omega.flac")
	if filepath.Base(filepath.Dir(newPath)) != "Geogaddi (2002)" {
		t.Errorf("newPath = %q, want it under a %q directory", newPath, wantSuffix)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("file not found at new path %s: %v", newPath, err)
	}
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Errorf("original file %s should no longer exist, stat err = %v", srcPath, err)
	}

	got, err := s.db.GetTrackFile(ctx, tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != newPath {
		t.Errorf("stored Path = %q, want %q", got.Path, newPath)
	}
	if got.OrganizedAt == nil {
		t.Error("OrganizedAt should be set")
	}
}

func TestOrganizeFileRefusesToOverwrite(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	artist, _ := s.db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	album, _ := s.db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	track, _ := s.db.GetOrCreateTrack(ctx, album.ID, "t-mbid", "Song", 1, 1, 1000)

	destDir := filepath.Join(rf.Path, "Artist", "Album (2020)")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(destDir, "01 - Song.mp3")
	if err := os.WriteFile(destPath, []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcPath := filepath.Join(rf.Path, "unsorted.mp3")
	if err := os.WriteFile(srcPath, []byte("new file"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, srcPath, 100, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tf.ID, &track.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	if _, err := s.OrganizeFile(ctx, tf.ID); err == nil {
		t.Error("expected an error when the destination already exists")
	}
	if _, err := os.Stat(srcPath); err != nil {
		t.Error("source file should be untouched after a refused organize")
	}
}

// TestPlanOrganizeArtistSkipsUnmatchedAndAlreadyOrganized proves the plan
// only lists files that actually need to move: an unmatched file (nothing
// to organize by) and a matched file already at its target path (a no-op
// move) are both left out, only the one file that genuinely needs
// relocating shows up.
func TestPlanOrganizeArtistSkipsUnmatchedAndAlreadyOrganized(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	artist, _ := s.db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	album, _ := s.db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	track1, _ := s.db.GetOrCreateTrack(ctx, album.ID, "t1-mbid", "Alpha and Omega", 3, 1, 200000)
	track2, _ := s.db.GetOrCreateTrack(ctx, album.ID, "t2-mbid", "Music Is Math", 4, 1, 200000)

	// Needs moving.
	unsorted := filepath.Join(rf.Path, "unsorted.flac")
	os.WriteFile(unsorted, []byte("x"), 0o644)
	tf1, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, unsorted, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tf1.ID, &track1.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	// Already at its target path — a no-op, must not appear in the plan.
	alreadyPath := filepath.Join(rf.Path, "Boards of Canada", "Geogaddi (2002)", "04 - Music Is Math.flac")
	if err := os.MkdirAll(filepath.Dir(alreadyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(alreadyPath, []byte("x"), 0o644)
	tf2, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, alreadyPath, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tf2.ID, &track2.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	// Unmatched — skipped, not an error.
	unmatchedPath := filepath.Join(rf.Path, "unmatched.flac")
	os.WriteFile(unmatchedPath, []byte("x"), 0o644)
	if _, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, unmatchedPath, 1, "flac", 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PlanOrganizeArtist(ctx, artist.ID)
	if err != nil {
		t.Fatalf("PlanOrganizeArtist: %v", err)
	}
	if len(plan) != 1 || plan[0].TrackFileID != tf1.ID || plan[0].From != unsorted {
		t.Fatalf("plan = %+v, want just tf1 (%d) moving from %s", plan, tf1.ID, unsorted)
	}
}

// TestPlanOrganizeArtistEmptyWhenNothingToDo mirrors LibriNode's "files
// already match the naming templates" empty-plan message — no matched
// files under the artist at all means an empty (not nil) plan.
func TestPlanOrganizeArtistEmptyWhenNothingToDo(t *testing.T) {
	s, _ := setupOrganizeScanner(t)
	ctx := t.Context()
	artist, _ := s.db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")

	plan, err := s.PlanOrganizeArtist(ctx, artist.ID)
	if err != nil {
		t.Fatalf("PlanOrganizeArtist: %v", err)
	}
	if plan == nil || len(plan) != 0 {
		t.Errorf("plan = %+v, want a non-nil empty slice", plan)
	}
}

// TestOrganizeArtistMovesFilesAndSurvivesPartialFailure proves the bulk
// apply moves every file its plan calls for, and that one file failing
// (here: a pre-existing file already occupying its destination, which
// OrganizeFile refuses to overwrite) doesn't stop the others from moving.
func TestOrganizeArtistMovesFilesAndSurvivesPartialFailure(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	artist, _ := s.db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	album, _ := s.db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	trackOK, _ := s.db.GetOrCreateTrack(ctx, album.ID, "t1-mbid", "Alpha and Omega", 3, 1, 200000)
	trackBlocked, _ := s.db.GetOrCreateTrack(ctx, album.ID, "t2-mbid", "Music Is Math", 4, 1, 200000)

	okSrc := filepath.Join(rf.Path, "ok.flac")
	os.WriteFile(okSrc, []byte("x"), 0o644)
	tfOK, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, okSrc, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tfOK.ID, &trackOK.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	blockedSrc := filepath.Join(rf.Path, "blocked.flac")
	os.WriteFile(blockedSrc, []byte("x"), 0o644)
	tfBlocked, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, blockedSrc, 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.SetTrackFileMatch(ctx, tfBlocked.ID, &trackBlocked.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
	// Occupy the blocked file's destination ahead of time so OrganizeFile
	// refuses to overwrite it.
	blockedDest := filepath.Join(rf.Path, "Boards of Canada", "Geogaddi (2002)", "04 - Music Is Math.flac")
	if err := os.MkdirAll(filepath.Dir(blockedDest), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(blockedDest, []byte("already here"), 0o644)

	moves, errs, err := s.OrganizeArtist(ctx, artist.ID)
	if err != nil {
		t.Fatalf("OrganizeArtist: %v", err)
	}
	if len(moves) != 1 || moves[0].TrackFileID != tfOK.ID {
		t.Errorf("moves = %+v, want just tfOK (%d)", moves, tfOK.ID)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %+v, want exactly 1 (the blocked file)", errs)
	}
	if _, err := os.Stat(okSrc); !os.IsNotExist(err) {
		t.Error("ok file should have moved off its original path")
	}
	if _, err := os.Stat(blockedSrc); err != nil {
		t.Error("blocked file should still be at its original path after a refused organize")
	}
}

func TestOrganizeFileRequiresMatch(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	ctx := t.Context()

	srcPath := filepath.Join(rf.Path, "unsorted.mp3")
	os.WriteFile(srcPath, []byte("x"), 0o644)
	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, srcPath, 1, "mp3", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.OrganizeFile(ctx, tf.ID); err == nil {
		t.Error("expected an error organizing an unmatched file")
	}
}
