package candidatesearch

import (
	"testing"

	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/release"
)

func TestScoreAndRankFiltersBlockedAndRanks(t *testing.T) {
	found := []indexer.Release{
		{Title: "Boards of Canada - Geogaddi FLAC", GUID: "good", Seeders: 5, Size: 400 << 20, DownloadURL: "http://x/good"},
		{Title: "Boards of Canada - Geogaddi Blocked FLAC", GUID: "blocked", Seeders: 5, Size: 400 << 20, DownloadURL: "http://x/blocked"},
		{Title: "Boards of Canada - Geogaddi MP3", GUID: "worse", Seeders: 5, Size: 400 << 20, DownloadURL: "http://x/worse"},
	}
	blocked := map[string]bool{"blocked": true}
	prefs := release.DefaultMusicPreferences()

	got := ScoreAndRank(found, blocked, prefs, "Boards of Canada", "")

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (blocked release dropped): %+v", len(got), got)
	}
	for _, c := range got {
		if c.GUID == "blocked" {
			t.Errorf("blocked release survived: %+v", c)
		}
	}
	// FLAC (100) outranks MP3 (70) — Rank must have been applied.
	if got[0].GUID != "good" {
		t.Errorf("got[0] = %+v, want the FLAC release ranked first", got[0])
	}
}

func TestScoreAndRankEmptyInput(t *testing.T) {
	got := ScoreAndRank(nil, nil, release.DefaultMusicPreferences(), "", "")
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
}

// TestScoreAndRankDedupesSameReleaseAcrossIndexers is the regression test
// for a real gap: the same release cross-posted on two configured
// indexers (or fanned out from two of Prowlarr's own sub-indexers) came
// back as two independently-scored candidates, with nothing here ever
// collapsing them — visibly duplicated rows in manual search, and wasted
// retry budget in autosearch's own retry loop. Two releases sharing a
// title (ignoring case/whitespace — the same rule IsBlocked already uses)
// must collapse to one even with distinct GUIDs (each indexer assigns its
// own), and a release repeated with the exact same GUID (the same
// indexer, listed twice) must collapse too.
func TestScoreAndRankDedupesSameReleaseAcrossIndexers(t *testing.T) {
	found := []indexer.Release{
		{Indexer: "indexer-a", Title: "Boards of Canada - Geogaddi FLAC", GUID: "a-1", Seeders: 5, Size: 400 << 20, DownloadURL: "http://a/1"},
		// Same release, different indexer, different GUID, cosmetically
		// different casing/whitespace in the title.
		{Indexer: "indexer-b", Title: "  boards of canada -  geogaddi  flac", GUID: "b-1", Seeders: 50, Size: 400 << 20, DownloadURL: "http://b/1"},
		// Same GUID repeated (one indexer listing it twice).
		{Indexer: "indexer-a", Title: "Boards of Canada - Geogaddi FLAC", GUID: "a-1", Seeders: 5, Size: 400 << 20, DownloadURL: "http://a/1"},
		// A genuinely different release must survive.
		{Indexer: "indexer-a", Title: "Boards of Canada - Campfire Headphase FLAC", GUID: "a-2", Seeders: 5, Size: 400 << 20, DownloadURL: "http://a/2"},
	}
	prefs := release.DefaultMusicPreferences()

	got := ScoreAndRank(found, nil, prefs, "Boards of Canada", "")

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (duplicates collapsed): %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, c := range got {
		seen[c.GUID] = true
	}
	if !seen["a-1"] || !seen["a-2"] {
		t.Errorf("got GUIDs = %v, want exactly the first Geogaddi duplicate (a-1) and the distinct Campfire Headphase release (a-2)", seen)
	}
}

// TestBuildQueryAvoidsDoublingASelfTitledAlbum is the regression test for
// half of the real wrong-album bug found live: a plain "artist + album"
// concatenation degenerates for a self-titled album into the same word
// repeated back to back ("Franz Ferdinand Franz Ferdinand"), handing the
// indexer's own search nothing that actually narrows by album. The other
// half — Score never checking the album title at all — is covered by
// release.TestScoreRejectsWrongAlbum.
func TestBuildQueryAvoidsDoublingASelfTitledAlbum(t *testing.T) {
	if got := BuildQuery("Franz Ferdinand", "Franz Ferdinand"); got != "Franz Ferdinand" {
		t.Errorf(`BuildQuery(self-titled) = %q, want "Franz Ferdinand" (not doubled)`, got)
	}
	// Case/whitespace variance between the artist and album rows (same
	// underlying MusicBrainz name, cosmetically different casing) must
	// still be recognized as the self-titled case.
	if got := BuildQuery("Franz Ferdinand", " franz ferdinand "); got != "Franz Ferdinand" {
		t.Errorf(`BuildQuery(self-titled, case/whitespace variant) = %q, want "Franz Ferdinand"`, got)
	}
	// An ordinary album must still combine both terms normally.
	if got := BuildQuery("Boards of Canada", "Geogaddi"); got != "Boards of Canada Geogaddi" {
		t.Errorf(`BuildQuery(ordinary album) = %q, want "Boards of Canada Geogaddi"`, got)
	}
}
