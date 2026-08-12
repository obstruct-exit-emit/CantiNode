package musicscanner

import (
	"testing"

	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// seedUnmatchedFile inserts a track_files row with no track_id (unmatched)
// and the given tags, backed by a real (empty) file on disk so path
// uniqueness/organizing concerns elsewhere never trip over a phantom path.
func seedUnmatchedFile(t *testing.T, s *Scanner, rf musiclibrary.RootFolder, relPath, tagsJSON string) int64 {
	t.Helper()
	tf, err := s.db.UpsertTrackFileByPath(rf.ID, rf.Path+"/"+relPath, 100, "flac", 0, 0, tagsJSON)
	if err != nil {
		t.Fatalf("seed unmatched file %s: %v", relPath, err)
	}
	return tf.ID
}

func testGeogaddiRelease() *musicbrainz.ReleaseWithTracklist {
	return &musicbrainz.ReleaseWithTracklist{
		ID:    "release-geogaddi",
		Title: "Geogaddi",
		Media: []musicbrainz.ReleaseMedium{
			{
				Position: 1,
				Tracks: []musicbrainz.ReleaseTrack{
					{Position: 1, Title: "Ready Lets Go", Recording: musicbrainz.Recording{ID: "rec-1", Title: "Ready Lets Go"}},
					{Position: 2, Title: "Music Is Math", Recording: musicbrainz.Recording{ID: "rec-2", Title: "Music Is Math"}},
					{Position: 3, Title: "Beware the Friendly Stranger", Recording: musicbrainz.Recording{ID: "rec-3", Title: "Beware the Friendly Stranger"}},
				},
			},
		},
	}
}

// TestSuggestMatchesByTrackNumber is the plain, expected case: a file
// whose own tags already carry a track number slots straight into the
// matching position, same as the automatic scan's own algorithm.
func TestSuggestMatchesByTrackNumber(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	id1 := seedUnmatchedFile(t, s, rf, "01.flac", `{"TrackNumber":1}`)
	id2 := seedUnmatchedFile(t, s, rf, "02.flac", `{"TrackNumber":2}`)

	got := s.SuggestMatches([]int64{id1, id2}, testGeogaddiRelease())

	if len(got) != 2 {
		t.Fatalf("suggestions = %+v, want 2", got)
	}
	byFile := map[int64]TrackSuggestion{}
	for _, sug := range got {
		byFile[sug.TrackFileID] = sug
	}
	if byFile[id1].RecordingMBID != "rec-1" || byFile[id1].TrackTitle != "Ready Lets Go" {
		t.Errorf("file 1 suggestion = %+v, want rec-1/Ready Lets Go", byFile[id1])
	}
	if byFile[id2].RecordingMBID != "rec-2" {
		t.Errorf("file 2 suggestion = %+v, want rec-2", byFile[id2])
	}
	if byFile[id1].ReleaseMBID != "release-geogaddi" {
		t.Errorf("ReleaseMBID = %q, want release-geogaddi", byFile[id1].ReleaseMBID)
	}
}

// TestSuggestMatchesByTitleWhenNoTrackNumber covers the fuzzy-title
// fallback for a file with no embedded track number at all.
func TestSuggestMatchesByTitleWhenNoTrackNumber(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	id := seedUnmatchedFile(t, s, rf, "a.flac", `{"Title":"Beware The Friendly Stranger"}`)

	got := s.SuggestMatches([]int64{id}, testGeogaddiRelease())

	if len(got) != 1 || got[0].RecordingMBID != "rec-3" {
		t.Fatalf("suggestions = %+v, want rec-3 (title match)", got)
	}
}

// TestSuggestMatchesNeverDoubleClaimsATrack confirms two files that would
// otherwise both slot onto the same track (e.g. identical/ambiguous tags)
// don't both get suggested it — the second is simply omitted, same as the
// automatic scan's own used-tracks bookkeeping.
func TestSuggestMatchesNeverDoubleClaimsATrack(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	id1 := seedUnmatchedFile(t, s, rf, "a.flac", `{"TrackNumber":1}`)
	id2 := seedUnmatchedFile(t, s, rf, "b.flac", `{"TrackNumber":1}`)

	got := s.SuggestMatches([]int64{id1, id2}, testGeogaddiRelease())

	if len(got) != 1 {
		t.Fatalf("suggestions = %+v, want exactly 1 (only one file may claim track 1)", got)
	}
}

// TestSuggestMatchesOmitsUnslottableFiles confirms a file with nothing
// usable (no track number, no recognizable title) is simply left out of
// the result rather than erroring the whole batch.
// TestListUnmatchedWithGroupsMergesMultiDiscFolders confirms
// ListUnmatchedWithGroups's groupKey agrees with the automatic scanner's
// own multi-disc merging — two CD1/CD2 subfolders of the same album share
// one groupKey (their parent), while an unrelated single-disc album keeps
// its own.
func TestListUnmatchedWithGroupsMergesMultiDiscFolders(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	cd1 := seedUnmatchedFile(t, s, rf, "The Wall/CD1/01.flac", `{"Artist":"Pink Floyd","Album":"The Wall"}`)
	cd2 := seedUnmatchedFile(t, s, rf, "The Wall/CD2/01.flac", `{"Artist":"Pink Floyd","Album":"The Wall"}`)
	other := seedUnmatchedFile(t, s, rf, "Wish You Were Here/01.flac", `{"Artist":"Pink Floyd","Album":"Wish You Were Here"}`)

	got, err := s.ListUnmatchedWithGroups()
	if err != nil {
		t.Fatalf("ListUnmatchedWithGroups: %v", err)
	}
	byID := map[int64]UnmatchedFileGroup{}
	for _, g := range got {
		byID[g.ID] = g
	}
	if len(byID) != 3 {
		t.Fatalf("got %d files, want 3", len(byID))
	}
	if byID[cd1].GroupKey != byID[cd2].GroupKey {
		t.Errorf("CD1/CD2 group keys = %q/%q, want equal", byID[cd1].GroupKey, byID[cd2].GroupKey)
	}
	if byID[other].GroupKey == byID[cd1].GroupKey {
		t.Errorf("unrelated album shares a group key with The Wall: %q", byID[other].GroupKey)
	}
}

func TestSuggestMatchesOmitsUnslottableFiles(t *testing.T) {
	s, rf := setupOrganizeScanner(t)
	good := seedUnmatchedFile(t, s, rf, "a.flac", `{"TrackNumber":1}`)
	bad := seedUnmatchedFile(t, s, rf, "b.flac", `{"Title":"Completely Unrelated Nonsense Xyz"}`)

	got := s.SuggestMatches([]int64{good, bad}, testGeogaddiRelease())

	if len(got) != 1 || got[0].TrackFileID != good {
		t.Fatalf("suggestions = %+v, want only the confidently-slotted file", got)
	}
}
