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

	got := ScoreAndRank(found, blocked, prefs)

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
	got := ScoreAndRank(nil, nil, release.DefaultMusicPreferences())
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
}
