package musicscanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

func TestCleanFolderName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Boards of Canada", "Boards of Canada"},
		{"Geogaddi [2002] [FLAC]", "Geogaddi"},
		{"The_Beatles", "The Beatles"},
		{"Abbey.Road.1969", "Abbey Road 1969"},
		{"  Extra   Spaces  ", "Extra Spaces"},
		{"(Deluxe Edition) Random Access Memories", "Random Access Memories"},
		{"Kind of Blue - Legacy Edition", "Kind of Blue - Legacy Edition"}, // hyphens are left alone, only brackets/underscores/dots are junk
	}
	for _, c := range cases {
		if got := cleanFolderName(c.in); got != c.want {
			t.Errorf("cleanFolderName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestArtistAlbumFromFilename(t *testing.T) {
	cases := []struct {
		path       string
		wantArtist string
		wantAlbum  string
	}{
		{"/x/Boards of Canada - Geogaddi - 01 - Alpha and Omega.flac", "Boards of Canada", "Geogaddi"},
		{"/x/Boards of Canada - Geogaddi - Alpha and Omega.flac", "Boards of Canada", "Geogaddi"},
		// Only two segments — too ambiguous to tell artist from title, left alone.
		{"/x/Boards of Canada - Alpha and Omega.flac", "", ""},
		// No separator at all.
		{"/x/01 Alpha and Omega.flac", "", ""},
		// Bracket junk in a segment gets cleaned the same as a folder name.
		{"/x/Boards of Canada - Geogaddi [FLAC] - 01 - Alpha and Omega.flac", "Boards of Canada", "Geogaddi"},
	}
	for _, c := range cases {
		artist, album := artistAlbumFromFilename(c.path)
		if artist != c.wantArtist || album != c.wantAlbum {
			t.Errorf("artistAlbumFromFilename(%q) = (%q, %q), want (%q, %q)", c.path, artist, album, c.wantArtist, c.wantAlbum)
		}
	}
}

func TestArtistAlbumFromPath(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	cases := []struct {
		name       string
		relPath    string
		wantArtist string
		wantAlbum  string
	}{
		{
			name:       "artist and album folders both present",
			relPath:    filepath.Join("Boards of Canada", "Geogaddi [2002] [FLAC]", "01 - Alpha and Omega.flac"),
			wantArtist: "Boards of Canada",
			wantAlbum:  "Geogaddi",
		},
		{
			name:       "flat layout, only an album folder",
			relPath:    filepath.Join("Geogaddi", "01 - Alpha and Omega.flac"),
			wantArtist: "",
			wantAlbum:  "Geogaddi",
		},
		{
			name:       "file sits directly in the root folder",
			relPath:    "01 - Alpha and Omega.flac",
			wantArtist: "",
			wantAlbum:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tf := &musiclibrary.TrackFile{
				RootFolderID: rf.ID,
				Path:         filepath.Join(rf.Path, c.relPath),
			}
			artist, album := s.artistAlbumFromPath(tf)
			if artist != c.wantArtist || album != c.wantAlbum {
				t.Errorf("artistAlbumFromPath(%q) = (%q, %q), want (%q, %q)", c.relPath, artist, album, c.wantArtist, c.wantAlbum)
			}
		})
	}
}

// TestMatchFileFuzzyFallsBackToFolderNames is the regression test for the
// feature this file exists for: a file with completely blank tags (not
// even a Title — matchFileFuzzy's own early-bail condition) used to never
// even attempt a search. Sitting the file in an Artist/Album folder
// structure now supplies the missing search terms, so the same file gets
// matched via SearchRecordings instead of being left untouched.
func TestMatchFileFuzzyFallsBackToFolderNames(t *testing.T) {
	searchResponse := []mbRecording{sampleRecording("rec-mbid", 95)}
	s, rf := newTestScanner(t, nil, searchResponse)
	ctx := t.Context()

	dir := filepath.Join(rf.Path, "Boards of Canada", "Geogaddi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	buildFLACFile(t, dir, "01 - Alpha and Omega.flac", map[string]string{}) // no tags at all

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1 — the folder-derived artist/album should have let a search happen", result.FilesMatched)
	}

	matched, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusMatched)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("len(matched) = %d, want 1", len(matched))
	}
}

// TestFolderTagConsensusFallsBackToFolderName confirms folderTagConsensus
// itself (not just matchFileFuzzy) reaches a real consensus from folder
// names when every file in the folder has blank Album/Artist tags — they
// all share the same immediate parent directory, so they trivially agree.
func TestFolderTagConsensusFallsBackToFolderName(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	dir := filepath.Join(rf.Path, "Boards of Canada", "Geogaddi")
	tf1 := &musiclibrary.TrackFile{RootFolderID: rf.ID, Path: filepath.Join(dir, "01 - Track One.flac")}
	tf2 := &musiclibrary.TrackFile{RootFolderID: rf.ID, Path: filepath.Join(dir, "02 - Track Two.flac")}

	entries := []folderEntry{
		{tf: tf1, tags: &tagreader.Tags{}},
		{tf: tf2, tags: &tagreader.Tags{}},
	}

	artist, album, ok := folderTagConsensus(entries, s.resolveArtistAlbumFallback)
	if !ok {
		t.Fatal("expected consensus via the shared folder name")
	}
	if artist != "Boards of Canada" || album != "Geogaddi" {
		t.Errorf("consensus = (%q, %q), want (\"Boards of Canada\", \"Geogaddi\")", artist, album)
	}
}

// TestFolderTagConsensusWithoutFallbackStillFailsOnBlankTags locks in the
// nil-fallback behavior every existing test in multidisc_test.go relies
// on: passing nil must skip the new folder/filename fallback entirely,
// preserving today's "no tags, no consensus" outcome exactly.
func TestFolderTagConsensusWithoutFallbackStillFailsOnBlankTags(t *testing.T) {
	tf := &musiclibrary.TrackFile{Path: "/x/Boards of Canada/Geogaddi/01 - Track.flac"}
	entries := []folderEntry{{tf: tf, tags: &tagreader.Tags{}}}

	if _, _, ok := folderTagConsensus(entries, nil); ok {
		t.Error("expected no consensus when fallback is nil, even though the path would resolve one")
	}
}
