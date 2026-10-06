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
		{"Fleetwood Mac", "Fleetwood Mac"},
		{"Rumours [1977] [FLAC]", "Rumours"},
		{"The_Wilderness", "The Wilderness"},
		{"Con.Todo.El.Mundo", "Con Todo El Mundo"},
		{"  Extra   Spaces  ", "Extra Spaces"},
		{"(Deluxe Edition) Discovery", "Discovery"},
		{"Kind of Blue - Legacy Edition", "Kind of Blue - Legacy Edition"}, // hyphens are left alone, only brackets/underscores/dots are junk
	}
	for _, c := range cases {
		if got := cleanFolderName(c.in); got != c.want {
			t.Errorf("cleanFolderName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestArtistAlbumFromFilename covers the filename fallback in isolation —
// "Above & Beyond - Group Therapy - ..." is the kind of flat, un-foldered
// name a loose download or a manually-added single file most often uses.
func TestArtistAlbumFromFilename(t *testing.T) {
	cases := []struct {
		path       string
		wantArtist string
		wantAlbum  string
	}{
		{"/x/Above & Beyond - Group Therapy - 01 - Sun and Moon.flac", "Above & Beyond", "Group Therapy"},
		{"/x/Above & Beyond - Group Therapy - Sun and Moon.flac", "Above & Beyond", "Group Therapy"},
		// Only two segments — too ambiguous to tell artist from title, left alone.
		{"/x/Above & Beyond - Sun and Moon.flac", "", ""},
		// No separator at all.
		{"/x/01 Sun and Moon.flac", "", ""},
		// Bracket junk in a segment gets cleaned the same as a folder name.
		{"/x/Above & Beyond - Group Therapy [FLAC] - 01 - Sun and Moon.flac", "Above & Beyond", "Group Therapy"},
	}
	for _, c := range cases {
		artist, album := artistAlbumFromFilename(c.path)
		if artist != c.wantArtist || album != c.wantAlbum {
			t.Errorf("artistAlbumFromFilename(%q) = (%q, %q), want (%q, %q)", c.path, artist, album, c.wantArtist, c.wantAlbum)
		}
	}
}

// TestArtistAlbumFromPath covers the folder fallback's three shapes with a
// different artist/album per case, so a copy-paste mistake in one case
// can't accidentally pass by matching another case's expectation instead.
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
			relPath:    filepath.Join("Fleetwood Mac", "Rumours [1977] [FLAC]", "02 - Dreams.flac"),
			wantArtist: "Fleetwood Mac",
			wantAlbum:  "Rumours",
		},
		{
			name:       "flat layout, only an album folder",
			relPath:    filepath.Join("Discovery", "01 - One More Time.flac"),
			wantArtist: "",
			wantAlbum:  "Discovery",
		},
		{
			name:       "single release folder encoding Artist - Album",
			relPath:    filepath.Join("Toro y Moi - Anything in Return [2013] [FLAC]", "01 - Harm in Change.flac"),
			wantArtist: "Toro y Moi",
			wantAlbum:  "Anything in Return",
		},
		{
			name:       "a folder name with more than one dash is left unsplit",
			relPath:    filepath.Join("Talking Heads - Stop Making Sense - Special Edition", "01 - Psycho Killer.flac"),
			wantArtist: "",
			wantAlbum:  "Talking Heads - Stop Making Sense - Special Edition",
		},
		{
			name:       "file sits directly in the root folder",
			relPath:    "01 - Some Loose Track.flac",
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

	dir := filepath.Join(rf.Path, "Tycho", "Dive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	buildFLACFile(t, dir, "01 - A Walk.flac", map[string]string{}) // no tags at all

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

// TestMatchFileFuzzyFallsBackToFilename is TestMatchFileFuzzyFallsBackToFolderNames'
// sibling for the *filename* fallback specifically — a file dropped
// straight into the root folder (no Artist/Album subfolders at all, so
// the folder fallback alone would find nothing) but whose own filename
// encodes Artist/Album/Title. Confirms the filename path alone is enough
// to unblock a search, independent of any folder structure.
func TestMatchFileFuzzyFallsBackToFilename(t *testing.T) {
	searchResponse := []mbRecording{sampleRecording("rec-mbid", 95)}
	s, rf := newTestScanner(t, nil, searchResponse)
	ctx := t.Context()

	buildFLACFile(t, rf.Path, "Explosions in the Sky - The Wilderness - 01 - Wilderness.flac", map[string]string{})

	result, err := s.ScanRootFolder(ctx, rf)
	if err != nil {
		t.Fatalf("ScanRootFolder: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if result.FilesMatched != 1 {
		t.Fatalf("FilesMatched = %d, want 1 — the filename-derived artist/album should have let a search happen", result.FilesMatched)
	}
}

// TestFolderTagConsensusFallsBackToFolderName confirms folderTagConsensus
// itself (not just matchFileFuzzy) reaches a real consensus from folder
// names when every file in the folder has blank Album/Artist tags — they
// all share the same immediate parent directory, so they trivially agree.
func TestFolderTagConsensusFallsBackToFolderName(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	dir := filepath.Join(rf.Path, "Khruangbin", "Con Todo El Mundo")
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
	if artist != "Khruangbin" || album != "Con Todo El Mundo" {
		t.Errorf("consensus = (%q, %q), want (\"Khruangbin\", \"Con Todo El Mundo\")", artist, album)
	}
}

// TestFolderTagConsensusMixedPartialTags covers a folder where files
// disagree on *how much* tagging they have, not just whether they have
// any at all: one file already carries a real Album tag (but no Artist),
// another has nothing whatsoever. The fallback must fill each gap
// per-file rather than only kicking in for an all-blank folder, and the
// result must still agree with the real Album tag already present.
func TestFolderTagConsensusMixedPartialTags(t *testing.T) {
	s, rf := newTestScanner(t, nil, nil)

	dir := filepath.Join(rf.Path, "Bonobo", "Migration")
	tf1 := &musiclibrary.TrackFile{RootFolderID: rf.ID, Path: filepath.Join(dir, "01 - Migration.flac")}
	tf2 := &musiclibrary.TrackFile{RootFolderID: rf.ID, Path: filepath.Join(dir, "02 - Break Apart.flac")}

	entries := []folderEntry{
		{tf: tf1, tags: &tagreader.Tags{Album: "Migration"}}, // real Album tag, no Artist
		{tf: tf2, tags: &tagreader.Tags{}},                   // nothing at all
	}

	artist, album, ok := folderTagConsensus(entries, s.resolveArtistAlbumFallback)
	if !ok {
		t.Fatal("expected consensus: the real Album tag and the folder-derived one agree")
	}
	if artist != "Bonobo" || album != "Migration" {
		t.Errorf("consensus = (%q, %q), want (\"Bonobo\", \"Migration\")", artist, album)
	}
}

// TestFilenameTrackFallback is the regression test for a real gap found
// live: a completely tagless release (every file's TrackNumber/Title/
// Artist/Album all blank — confirmed against a real torrent) left every
// file unmatched even once the whole folder's own release was confidently
// resolved, because slotTrack had nothing at all to go on. Covers the
// extraction itself in isolation — filenameTrackFallback's interaction
// with slotTrack's own sanity check is covered separately in
// folder_match_test.go.
func TestFilenameTrackFallback(t *testing.T) {
	cases := []struct {
		name          string
		path          string
		in            tagreader.Tags
		wantNum       int
		wantTitle     string
		wantUnchanged bool // true: the exact same *Tags pointer must come back untouched
	}{
		{name: "dash separator", path: "/x/11 - 40'.flac", in: tagreader.Tags{}, wantNum: 11, wantTitle: "40'"},
		{name: "dot separator", path: "/x/01. Come on Home.flac", in: tagreader.Tags{}, wantNum: 1, wantTitle: "Come on Home"},
		{name: "space separator", path: "/x/03 Michael.flac", in: tagreader.Tags{}, wantNum: 3, wantTitle: "Michael"},
		{name: "leading zeros stripped", path: "/x/007 Jump.flac", in: tagreader.Tags{}, wantNum: 7, wantTitle: "Jump"},
		{
			name: "embedded TrackNumber always wins, filename ignored",
			path: "/x/11 - 40'.flac", in: tagreader.Tags{TrackNumber: 5, Title: "Real Title"},
			wantNum: 5, wantTitle: "Real Title", wantUnchanged: true,
		},
		{
			name: "existing Title is never overwritten by the filename guess",
			path: "/x/11 - 40'.flac", in: tagreader.Tags{Title: "Embedded Title"},
			wantNum: 11, wantTitle: "Embedded Title",
		},
		{name: "number glued straight to the next word, no real separator", path: "/x/128kbps Rip.flac", in: tagreader.Tags{}, wantNum: 0, wantUnchanged: true},
		{name: "4-digit number too long to be a real track number", path: "/x/2024 Remaster.flac", in: tagreader.Tags{}, wantNum: 0, wantUnchanged: true},
		{name: "no leading number at all", path: "/x/Intro.flac", in: tagreader.Tags{}, wantNum: 0, wantUnchanged: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filenameTrackFallback(&c.in, c.path)
			if c.wantUnchanged {
				if got != &c.in {
					t.Fatalf("expected the exact same *Tags pointer back unchanged, got a new one: %+v", got)
				}
				return
			}
			if got.TrackNumber != c.wantNum {
				t.Errorf("TrackNumber = %d, want %d", got.TrackNumber, c.wantNum)
			}
			if got.Title != c.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, c.wantTitle)
			}
		})
	}
}

// TestFolderTagConsensusWithoutFallbackStillFailsOnBlankTags locks in the
// nil-fallback behavior every existing test in multidisc_test.go relies
// on: passing nil must skip the new folder/filename fallback entirely,
// preserving today's "no tags, no consensus" outcome exactly.
func TestFolderTagConsensusWithoutFallbackStillFailsOnBlankTags(t *testing.T) {
	tf := &musiclibrary.TrackFile{Path: "/x/Air/Moon Safari/01 - Track.flac"}
	entries := []folderEntry{{tf: tf, tags: &tagreader.Tags{}}}

	if _, _, ok := folderTagConsensus(entries, nil); ok {
		t.Error("expected no consensus when fallback is nil, even though the path would resolve one")
	}
}
