package musicscanner

import (
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// entry builds a folderEntry for a synthetic file at path, tagged with
// artist/album (both empty means "no consensus signal from this file").
func entry(id int64, path, artist, album string, discNumber int) folderEntry {
	return folderEntry{
		tf:   &musiclibrary.TrackFile{ID: id, Path: path},
		tags: &tagreader.Tags{Artist: artist, Album: album, DiscNumber: discNumber},
	}
}

// TestGroupMultiDiscFoldersMergesSameAlbumDiscs is the core case: two
// CD1/CD2 subfolders whose files agree on the same Artist+Album collapse
// into one group under their shared parent, with each file's disc number
// inferred from its folder name.
func TestGroupMultiDiscFoldersMergesSameAlbumDiscs(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/The Wall/CD1": {
			entry(1, "/music/The Wall/CD1/01.flac", "Pink Floyd", "The Wall", 0),
			entry(2, "/music/The Wall/CD1/02.flac", "Pink Floyd", "The Wall", 0),
		},
		"/music/The Wall/CD2": {
			entry(3, "/music/The Wall/CD2/01.flac", "Pink Floyd", "The Wall", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	if len(got) != 1 {
		t.Fatalf("groups = %+v, want exactly 1 merged group", got)
	}
	merged, ok := got["/music/The Wall"]
	if !ok {
		t.Fatalf("got = %+v, want key /music/The Wall", got)
	}
	if len(merged) != 3 {
		t.Fatalf("merged entries = %d, want 3", len(merged))
	}
	discByID := map[int64]int{}
	for _, e := range merged {
		discByID[e.tf.ID] = e.tags.DiscNumber
	}
	if discByID[1] != 1 || discByID[2] != 1 {
		t.Errorf("CD1 files disc numbers = %+v, want both 1", discByID)
	}
	if discByID[3] != 2 {
		t.Errorf("CD2 file disc number = %d, want 2", discByID[3])
	}
}

// TestGroupMultiDiscFoldersRejectsMismatchedArtistWhenOneSideBlank is the
// regression test for a real gap found in review: the artist-agreement
// check only ever fired when BOTH sides had a non-empty artist tag and
// disagreed — a disc with no artist tag at all (common for poorly-tagged
// rips) skipped the check entirely, so it could merge into a completely
// different album/artist that happened to share a post-suffix-strip
// title. One side blank and the other populated must now count as a
// mismatch, not a free pass.
func TestGroupMultiDiscFoldersRejectsMismatchedArtistWhenOneSideBlank(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Box Set/CD1": {
			// No artist tag at all on this disc's files.
			entry(1, "/music/Box Set/CD1/01.flac", "", "Greatest Hits CD 1", 0),
		},
		"/music/Box Set/CD2": {
			// A genuinely different release that happens to share a
			// post-strip album title.
			entry(2, "/music/Box Set/CD2/01.flac", "Various Artists", "Greatest Hits CD 2", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	if len(got) != 2 {
		t.Fatalf("groups = %+v, want 2 separate groups (one side has no artist signal, the other does — must not merge)", got)
	}
}

// TestGroupMultiDiscFoldersKeepsLooseFilesInParent is the regression test
// for a severe bug: a loose file sitting directly in the album's parent
// directory (e.g. a bonus track dropped outside CD1/CD2) shares its
// group's own key ("parent") with the merge target — the catch-all loop
// that copies through every group not already merged used to overwrite
// out[parent] with just that loose file, silently discarding every track
// the CD1/CD2 merge had just combined. The loose file must survive
// alongside the merged disc tracks, not replace them.
func TestGroupMultiDiscFoldersKeepsLooseFilesInParent(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/The Wall/CD1": {
			entry(1, "/music/The Wall/CD1/01.flac", "Pink Floyd", "The Wall", 0),
		},
		"/music/The Wall/CD2": {
			entry(2, "/music/The Wall/CD2/01.flac", "Pink Floyd", "The Wall", 0),
		},
		"/music/The Wall": {
			entry(3, "/music/The Wall/Bonus Track.flac", "Pink Floyd", "The Wall", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	merged, ok := got["/music/The Wall"]
	if !ok {
		t.Fatalf("got = %+v, want a merged group at the parent", got)
	}
	ids := map[int64]bool{}
	for _, e := range merged {
		ids[e.tf.ID] = true
	}
	if len(merged) != 3 || !ids[1] || !ids[2] || !ids[3] {
		t.Fatalf("merged entries = %+v (ids %v), want all 3 files (CD1, CD2, and the loose parent file)", merged, ids)
	}
}

// TestGroupMultiDiscFoldersToleratesPerDiscAlbumSuffix is the regression
// test for a real-world case found live in production: a rip that tags
// each disc's own Album field with a disc-number qualifier ("Moonglow CD
// 1" / "Moonglow CD 2") rather than an identical Album tag across discs.
// Exact-match consensus would wrongly treat these as two different albums
// and never merge them — stripDiscSuffix must normalize both down to
// "Moonglow" before comparing.
func TestGroupMultiDiscFoldersToleratesPerDiscAlbumSuffix(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Avantasia - Moonglow (2CD)/CD1": {
			entry(1, "/music/Avantasia - Moonglow (2CD)/CD1/01.mp3", "Avantasia", "Moonglow CD 1", 0),
		},
		"/music/Avantasia - Moonglow (2CD)/CD2": {
			entry(2, "/music/Avantasia - Moonglow (2CD)/CD2/11.mp3", "Avantasia", "Moonglow CD 2", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	merged, ok := got["/music/Avantasia - Moonglow (2CD)"]
	if !ok || len(merged) != 2 {
		t.Fatalf("got = %+v, want both discs merged under their shared parent", got)
	}
	discByID := map[int64]int{}
	for _, e := range merged {
		discByID[e.tf.ID] = e.tags.DiscNumber
	}
	if discByID[1] != 1 || discByID[2] != 2 {
		t.Errorf("disc numbers = %+v, want {1:1, 2:2}", discByID)
	}
}

func TestStripDiscSuffix(t *testing.T) {
	cases := map[string]string{
		"Moonglow CD 1":         "Moonglow",
		"Moonglow CD 2":         "Moonglow",
		"Moonglow (Disc 1)":     "Moonglow",
		"Moonglow [Disc 2]":     "Moonglow",
		"Moonglow - CD2":        "Moonglow",
		"CD1 - Moonglow":        "Moonglow",
		"CD 2: Moonglow":        "Moonglow",
		"Disc1 Moonglow":        "Moonglow",
		"Wish You Were Here":    "Wish You Were Here",
		"The Wall":              "The Wall",
		"CD Player Repair Disc": "CD Player Repair Disc", // no disc-number qualifier at all
	}
	for in, want := range cases {
		if got := stripDiscSuffix(in); got != want {
			t.Errorf("stripDiscSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// entryWithAlbumArtist is entry's sibling for cases that need to control
// AlbumArtist separately from Artist — folderTagConsensus prefers
// AlbumArtist when present, exactly the distinction the Various Artists
// inference tests below need to exercise.
func entryWithAlbumArtist(id int64, path, artist, albumArtist, album string) folderEntry {
	return folderEntry{
		tf:   &musiclibrary.TrackFile{ID: id, Path: path},
		tags: &tagreader.Tags{Artist: artist, AlbumArtist: albumArtist, Album: album},
	}
}

// TestFolderTagConsensusVariousArtistsInference is the regression test for
// a real gap: a compilation with no consistent AlbumArtist override (only
// each track's own, genuinely different, Artist tag) used to fail
// consensus outright and fall back to weak per-file fuzzy search, even
// though the album title agreeing across every file is exactly the signal
// a real compilation gives.
func TestFolderTagConsensusVariousArtistsInference(t *testing.T) {
	entries := []folderEntry{
		entry(1, "/music/Now/01.mp3", "Phil Collins", "Now That's What I Call Music", 0),
		entry(2, "/music/Now/02.mp3", "Duran Duran", "Now That's What I Call Music", 0),
		entry(3, "/music/Now/03.mp3", "UB40", "Now That's What I Call Music", 0),
	}
	artist, album, ok := folderTagConsensus(entries)
	if !ok {
		t.Fatalf("folderTagConsensus ok = false, want true (inferred Various Artists)")
	}
	if artist != "Various Artists" {
		t.Errorf("artist = %q, want %q", artist, "Various Artists")
	}
	if album != "Now That's What I Call Music" {
		t.Errorf("album = %q, want the agreed-on title", album)
	}
}

// TestFolderTagConsensusAlreadyTaggedVariousArtists confirms a folder
// that's already properly tagged (AlbumArtist = "Various Artists" on
// every file) keeps working exactly as before — it's just an ordinary
// agreeing artist, no inference needed.
func TestFolderTagConsensusAlreadyTaggedVariousArtists(t *testing.T) {
	entries := []folderEntry{
		entryWithAlbumArtist(1, "/music/Now/01.mp3", "Phil Collins", "Various Artists", "Now"),
		entryWithAlbumArtist(2, "/music/Now/02.mp3", "Duran Duran", "Various Artists", "Now"),
	}
	artist, album, ok := folderTagConsensus(entries)
	if !ok || artist != "Various Artists" || album != "Now" {
		t.Errorf("folderTagConsensus = %q, %q, %v, want Various Artists, Now, true", artist, album, ok)
	}
}

// TestFolderTagConsensusRejectsDisagreeingAlbumArtist confirms an
// inconsistent explicit AlbumArtist (not merely absent) still fails
// consensus outright rather than being guessed at as a compilation —
// more likely broken/conflicting tagging than a real one.
func TestFolderTagConsensusRejectsDisagreeingAlbumArtist(t *testing.T) {
	entries := []folderEntry{
		entryWithAlbumArtist(1, "/music/X/01.mp3", "", "Compilation Artist A", "Mixed Tape"),
		entryWithAlbumArtist(2, "/music/X/02.mp3", "", "Compilation Artist B", "Mixed Tape"),
	}
	if _, _, ok := folderTagConsensus(entries); ok {
		t.Error("folderTagConsensus ok = true, want false — disagreeing AlbumArtist should not infer Various Artists")
	}
}

// TestFolderTagConsensusRejectsSingleArtistDisagreement confirms a folder
// where only two files disagree and album doesn't even agree stays a hard
// failure — no compilation signal at all, just mismatched content.
func TestFolderTagConsensusRejectsSingleArtistDisagreement(t *testing.T) {
	entries := []folderEntry{
		entry(1, "/music/X/01.mp3", "Artist A", "Album One", 0),
		entry(2, "/music/X/02.mp3", "Artist B", "Album Two", 0),
	}
	if _, _, ok := folderTagConsensus(entries); ok {
		t.Error("folderTagConsensus ok = true, want false — no shared album at all")
	}
}

// TestGroupMultiDiscFoldersKeepsDifferentAlbumsSeparate confirms a bundle
// of genuinely different albums (a discography/box-set dump, each in its
// own disc-pattern-named subfolder purely by coincidence) is NOT merged —
// merging would search MusicBrainz using tags naming two different albums
// at once.
func TestGroupMultiDiscFoldersKeepsDifferentAlbumsSeparate(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Box Set/CD1": {
			entry(1, "/music/Box Set/CD1/01.flac", "Artist", "Album One", 0),
		},
		"/music/Box Set/CD2": {
			entry(2, "/music/Box Set/CD2/01.flac", "Artist", "Album Two", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	if len(got) != 2 {
		t.Fatalf("groups = %+v, want 2 separate groups (different albums)", got)
	}
	if _, ok := got["/music/Box Set/CD1"]; !ok {
		t.Errorf("CD1 group missing, got %+v", got)
	}
	if _, ok := got["/music/Box Set/CD2"]; !ok {
		t.Errorf("CD2 group missing, got %+v", got)
	}
}

// TestGroupMultiDiscFoldersLeavesNonDiscFoldersAlone confirms an ordinary
// single-disc album folder (no CD1/CD2 siblings at all) passes through
// completely unchanged.
func TestGroupMultiDiscFoldersLeavesNonDiscFoldersAlone(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Wish You Were Here": {
			entry(1, "/music/Wish You Were Here/01.flac", "Pink Floyd", "Wish You Were Here", 0),
		},
	}

	got := groupMultiDiscFolders(groups)

	if len(got) != 1 || len(got["/music/Wish You Were Here"]) != 1 {
		t.Fatalf("got = %+v, want the single group unchanged", got)
	}
}

// TestGroupMultiDiscFoldersRespectsExistingDiscNumberTag confirms a file
// that already carries its own DiscNumber tag is never overridden by the
// folder-name inference — tags always win when present.
func TestGroupMultiDiscFoldersRespectsExistingDiscNumberTag(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Album/CD1": {
			entry(1, "/music/Album/CD1/01.flac", "Artist", "Album", 9), // already tagged disc 9
		},
		"/music/Album/CD2": {
			entry(2, "/music/Album/CD2/01.flac", "Artist", "Album", 0),
		},
	}

	got := groupMultiDiscFolders(groups)
	merged := got["/music/Album"]
	discByID := map[int64]int{}
	for _, e := range merged {
		discByID[e.tf.ID] = e.tags.DiscNumber
	}
	if discByID[1] != 9 {
		t.Errorf("file with existing DiscNumber tag = %d, want unchanged 9", discByID[1])
	}
	if discByID[2] != 2 {
		t.Errorf("file with no DiscNumber tag = %d, want inferred 2", discByID[2])
	}
}

// TestGroupMultiDiscFoldersRequiresAtLeastTwoSiblings confirms a single
// lone "CD1"-named folder (no CD2, CD3, etc. sibling) isn't merged with
// anything — there's nothing to merge it with, so it passes through as its
// own group unchanged.
func TestGroupMultiDiscFoldersRequiresAtLeastTwoSiblings(t *testing.T) {
	groups := map[string][]folderEntry{
		"/music/Album/CD1": {
			entry(1, "/music/Album/CD1/01.flac", "Artist", "Album", 0),
		},
	}

	got := groupMultiDiscFolders(groups)
	if len(got) != 1 || len(got["/music/Album/CD1"]) != 1 {
		t.Fatalf("got = %+v, want the lone CD1 folder unchanged", got)
	}
}

func TestInferDiscNumber(t *testing.T) {
	cases := map[string]int{
		"CD1": 1, "CD 2": 2, "Disc1": 1, "Disc 03": 3, "disk2": 2,
		"D1": 1, "Disco Inferno": 0, "CD": 0, "Album": 0,
	}
	for name, want := range cases {
		if got := inferDiscNumber(name); got != want {
			t.Errorf("inferDiscNumber(%q) = %d, want %d", name, got, want)
		}
	}
}
