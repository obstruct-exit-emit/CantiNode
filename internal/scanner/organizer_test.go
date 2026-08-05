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
