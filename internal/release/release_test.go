package release

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Parsed
	}{
		{
			"Boards of Canada - Geogaddi (2002) Retail FLAC",
			Parsed{Author: "Boards of Canada", Title: "Geogaddi", Year: 2002, Formats: []string{"flac"}, Retail: true},
		},
		{
			"Boards.of.Canada.-.Geogaddi.2002.Retail.FLAC-GROUP",
			Parsed{Author: "Boards of Canada", Title: "Geogaddi", Year: 2002, Formats: []string{"flac"}, Retail: true, Group: "GROUP"},
		},
		{
			"Geogaddi by Boards of Canada MP3",
			Parsed{Author: "Boards of Canada", Title: "Geogaddi", Formats: []string{"mp3"}},
		},
		{
			"Der Geogaddi (German) MP3",
			Parsed{Title: "Der Geogaddi", Formats: []string{"mp3"}, Language: "german"},
		},
		{
			"Some Linux ISO x264-GRP",
			Parsed{Title: "Some Linux ISO x264-GRP"},
		},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

func rel(title string, protocol string, size int64, seeders int) indexer.Release {
	return indexer.Release{
		Indexer: "mock", Protocol: protocol, Title: title,
		// Real indexer releases always carry a download link; a release
		// without one is rejected as ungrabbable (see Score).
		DownloadURL: "https://mock.example/get/" + title,
		Size:        size, Seeders: seeders, Peers: seeders,
	}
}

func TestScoreMusic(t *testing.T) {
	prefs := DefaultMusicPreferences()

	flac := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs)
	if !flac.Approved {
		t.Fatalf("flac rejected: %v", flac.Rejections)
	}
	// flac 100 + usenet 10
	if flac.Score != 110 {
		t.Errorf("flac score = %d, want 110", flac.Score)
	}

	mp3 := Score(rel("Boards of Canada - Geogaddi MP3", indexer.ProtocolUsenet, 100<<20, -1), prefs)
	if !mp3.Approved {
		t.Errorf("mp3 should approve: %v", mp3.Rejections)
	}
	if mp3.Score >= flac.Score {
		t.Errorf("mp3 (%d) should rank below flac (%d)", mp3.Score, flac.Score)
	}

	epub := Score(rel("Boards of Canada - Geogaddi EPUB", indexer.ProtocolUsenet, 400<<20, -1), prefs)
	if epub.Approved {
		t.Error("non-music format should be rejected under music prefs")
	}

	noFormat := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs)
	if noFormat.Approved {
		t.Error("release without a format should be rejected")
	}

	dead := Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 0), prefs)
	if dead.Approved {
		t.Error("torrent with 0 seeders should be rejected")
	}

	seeded := Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 50), prefs)
	if !seeded.Approved || seeded.Score != 120 { // 100 + capped 20
		t.Errorf("seeded torrent = %+v, want score 120", seeded)
	}

	tiny := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 1<<10, -1), prefs)
	if tiny.Approved {
		t.Error("1 KiB flac release should be rejected as suspiciously small")
	}

	huge := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 8<<30, -1), prefs)
	if huge.Approved {
		t.Error("8 GiB single release should be rejected as too large")
	}
}

func TestScoreRejectsSpamNamedExecutable(t *testing.T) {
	prefs := DefaultMusicPreferences()
	spam := Score(rel("Geogaddi FLAC Setup.exe", indexer.ProtocolUsenet, 400<<20, -1), prefs)
	if spam.Approved {
		t.Error("release naming an executable should be rejected")
	}
}

func TestPreferencesFromProfile(t *testing.T) {
	prefs := PreferencesFromProfile(library.QualityProfile{
		Formats:     []string{"flac", "mp3"},
		Language:    "german",
		RetailBonus: 40,
		MinSize:     100,
		MaxSize:     1000,
	})
	if prefs.FormatScores["flac"] != 100 || prefs.FormatScores["mp3"] != 80 {
		t.Errorf("format scores = %v", prefs.FormatScores)
	}
	if _, ok := prefs.FormatScores["wav"]; ok {
		t.Error("unlisted format should be absent (rejected)")
	}

	// A flac/mp3-only German profile rejects English wav, prefers flac.
	wav := Score(rel("Geogaddi WAV", indexer.ProtocolUsenet, 500, -1), prefs)
	if wav.Approved {
		t.Errorf("wav approved under flac/mp3 profile: %+v", wav)
	}
	flac := Score(rel("Der Geogaddi FLAC German Retail", indexer.ProtocolUsenet, 500, -1), prefs)
	if !flac.Approved || flac.Score != 150 { // 100 + retail 40 + usenet 10
		t.Errorf("flac = %+v, want approved score 150", flac)
	}

	// Long format lists floor at 20.
	many := PreferencesFromProfile(library.QualityProfile{
		Formats: []string{"flac", "wav", "mp3", "m4a", "opus", "aac"},
	})
	if many.FormatScores["opus"] != 20 || many.FormatScores["aac"] != 20 {
		t.Errorf("floored scores = %v", many.FormatScores)
	}
}

// TestPreferencesForAllowsUnstatedFormat is the regression case for a real
// bug: real-world music release titles routinely name the source rather
// than the codec ("SHM-CD", "24-96 hdtracks", "4CD Box" — all observed
// directly from a live Prowlarr search, none of them containing
// flac/mp3/m4a/opus/wav), so PreferencesFor must not leave AllowUnknownFormat
// at its zero value — every real search was coming back with zero approved
// results before this was fixed.
func TestPreferencesForAllowsUnstatedFormat(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	prefs := PreferencesFor(store, "music")
	if !prefs.AllowUnknownFormat {
		t.Fatal("PreferencesFor(music) must allow an unstated format")
	}

	real := Score(rel("Derek and the Dominos-Layla and Other Assorted Love Songs-REMASTERED SHM-CD-2013-JRP",
		indexer.ProtocolUsenet, 163060320, -1), prefs)
	if !real.Approved {
		t.Errorf("real-world format-less release should approve: %+v", real)
	}
}

func TestRank(t *testing.T) {
	prefs := DefaultMusicPreferences()
	candidates := []Candidate{
		Score(rel("Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs),               // rejected
		Score(rel("Geogaddi MP3", indexer.ProtocolUsenet, 400<<20, -1), prefs),           // low
		Score(rel("Geogaddi Retail FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs),   // high
		Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 5), prefs),          // mid
	}
	Rank(candidates)
	if !candidates[0].Approved {
		t.Errorf("first = %+v", candidates[0])
	}
	if candidates[len(candidates)-1].Approved {
		t.Error("rejected candidate should sort last")
	}
	for i := 1; i < 3; i++ {
		if candidates[i-1].Score < candidates[i].Score {
			t.Errorf("approved candidates out of order at %d", i)
		}
	}
}
