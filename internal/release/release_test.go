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
		// Found live: the loose word-scan absorbed a bare "It" as the
		// 2-letter Italian code with no ambiguity guard at all, both
		// mistagging the language and stripping the word out of the title.
		// "it"/"de"/"es"/"en"/"fr"/"nl" are all ordinary English words —
		// only trustworthy as a language code inside a delimited tag.
		{
			"Say It Ain't So [FLAC]",
			Parsed{Title: "Say It Ain't So", Formats: []string{"flac"}},
		},
		// The bracketed form must still work — "[EN]" unambiguously means
		// English there, unlike a bare "En" floating in title text.
		{
			"Geogaddi [EN][FLAC]",
			Parsed{Title: "Geogaddi", Formats: []string{"flac"}, Language: "english"},
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

	flac := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if !flac.Approved {
		t.Fatalf("flac rejected: %v", flac.Rejections)
	}
	// flac 100 + usenet 10
	if flac.Score != 110 {
		t.Errorf("flac score = %d, want 110", flac.Score)
	}

	mp3 := Score(rel("Boards of Canada - Geogaddi MP3", indexer.ProtocolUsenet, 100<<20, -1), prefs, "", "")
	if !mp3.Approved {
		t.Errorf("mp3 should approve: %v", mp3.Rejections)
	}
	if mp3.Score >= flac.Score {
		t.Errorf("mp3 (%d) should rank below flac (%d)", mp3.Score, flac.Score)
	}

	epub := Score(rel("Boards of Canada - Geogaddi EPUB", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if epub.Approved {
		t.Error("non-music format should be rejected under music prefs")
	}

	noFormat := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if noFormat.Approved {
		t.Error("release without a format should be rejected")
	}

	dead := Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 0), prefs, "", "")
	if dead.Approved {
		t.Error("torrent with 0 seeders should be rejected")
	}

	seeded := Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 50), prefs, "", "")
	if !seeded.Approved || seeded.Score != 120 { // 100 + capped 20
		t.Errorf("seeded torrent = %+v, want score 120", seeded)
	}

	tiny := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 1<<10, -1), prefs, "", "")
	if tiny.Approved {
		t.Error("1 KiB flac release should be rejected as suspiciously small")
	}

	huge := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 8<<30, -1), prefs, "", "")
	if huge.Approved {
		t.Error("8 GiB single release should be rejected as too large")
	}
}

// TestScoreUpgradeRejectsFormatLessRelease is the regression test for a
// real bug: an upgrade search (MinFormatScore set) used to approve any
// release whose title stated no recognizable format at all, because that
// branch never reached the MinFormatScore rejection — and
// AllowUnknownFormat is forced on for every real search (see
// PreferencesFor), so this wasn't an edge case, it was the common one:
// real indexer results routinely omit the codec from the title.
func TestScoreUpgradeRejectsFormatLessRelease(t *testing.T) {
	prefs := DefaultMusicPreferences()
	prefs.AllowUnknownFormat = true
	prefs.MinFormatScore = prefs.FormatScores["flac"] // pretend flac is already owned

	c := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if c.Approved {
		t.Errorf("format-less release must not approve as an upgrade over a known-good owned format: %+v", c)
	}

	// A format-less release is still fine to approve for a PLAIN search
	// (MinFormatScore unset) — this must not regress into rejecting every
	// format-less release outright.
	plain := DefaultMusicPreferences()
	plain.AllowUnknownFormat = true
	ok := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), plain, "", "")
	if !ok.Approved {
		t.Errorf("format-less release should still approve for a plain (non-upgrade) search: %+v", ok)
	}
}

// TestScoreUpgradeRejectsFormatLessReleaseAtFloor is the regression test
// for a gap the original fix above missed: the rejection used to only fire
// when the fixed unknownFormatScore baseline (30) didn't clear
// MinFormatScore, which stopped protecting anything once MinFormatScore
// itself dropped below 30 — the case for whoever owns the worst-ranked
// format in a full 5-format profile (scored 100/80/60/40/20 by list
// position, floored at 20). 30 > 20 let a format-less release auto-approve
// as a "genuine upgrade" there purely from that coincidence, never from
// real evidence the release was actually better.
func TestScoreUpgradeRejectsFormatLessReleaseAtFloor(t *testing.T) {
	prefs := Preferences{
		FormatScores:       map[string]int{"flac": 100, "wav": 80, "mp3": 60, "m4a": 40, "opus": 20},
		AllowUnknownFormat: true,
		MinFormatScore:     20, // owned format is opus, the worst-ranked
		MinSize:            1 << 20,
		MaxSize:            4 << 30,
	}

	c := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if c.Approved {
		t.Errorf("format-less release must not approve as an upgrade just because the owned format is the profile's worst-ranked: %+v", c)
	}
}

func TestScoreRejectsSpamNamedExecutable(t *testing.T) {
	prefs := DefaultMusicPreferences()
	spam := Score(rel("Geogaddi FLAC Setup.exe", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", "")
	if spam.Approved {
		t.Error("release naming an executable should be rejected")
	}
}

// TestScoreRejectsWrongArtist is the regression test for a real bug found
// live: nothing in Score ever compared a candidate release against who was
// actually being searched for, so a release that merely shared a word
// with the wanted album — Nat King Cole's own "Moonglow" surfacing for a
// search for Avantasia's wanted album "Moonglow" — could score purely on
// format/size/health merits and get auto-grabbed by autosearch. Score must
// now reject a release whose title doesn't plausibly name the wanted
// artist at all.
func TestScoreRejectsWrongArtist(t *testing.T) {
	prefs := DefaultMusicPreferences()

	wrongArtist := Score(rel("Nat King Cole-Moonglow-3CD-FLAC-1995-LoKET", indexer.ProtocolUsenet, 400<<20, -1), prefs, "Avantasia", "")
	if wrongArtist.Approved {
		t.Errorf("release for a different artist must not approve: %+v", wrongArtist)
	}

	rightArtist := Score(rel("Tobias Sammets Avantasia - Moonglow 2CD FLAC 2019", indexer.ProtocolUsenet, 400<<20, -1), prefs, "Avantasia", "")
	if !rightArtist.Approved {
		t.Errorf("release actually naming the wanted artist should still approve: %+v", rightArtist.Rejections)
	}
}

// TestScoreRejectsWrongAlbum is the regression test for artistRelevant's
// own missing other half, found live against a real wrong-album grab:
// searching for Franz Ferdinand's self-titled album "Franz Ferdinand"
// surfaced their unrelated, more recent "The Human Fear" instead, and
// nothing in Score ever checked the ALBUM title — only the artist. A
// release correctly naming the artist but a different album must still be
// rejected; one naming the actual wanted album must still approve.
func TestScoreRejectsWrongAlbum(t *testing.T) {
	prefs := DefaultMusicPreferences()

	wrongAlbum := Score(rel("Franz Ferdinand - The Human Fear (2025) FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs, "Franz Ferdinand", "Franz Ferdinand")
	if wrongAlbum.Approved {
		t.Errorf("release for a different album by the right artist must not approve: %+v", wrongAlbum)
	}

	rightAlbum := Score(rel("Franz Ferdinand - Franz Ferdinand (2004) FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs, "Franz Ferdinand", "Franz Ferdinand")
	if !rightAlbum.Approved {
		t.Errorf("release actually naming the wanted album should still approve: %+v", rightAlbum.Rejections)
	}
}

// TestScoreRejectsWrongAlbumWhenArtistNameIsACommonWord is the regression
// test for a gap the fix above missed, found live during a later burn-in
// pass: searching for The Beatles' self-titled "The Beatles" (the White
// Album) still approved "Beatles VI", "Meet The Beatles!", and "With The
// Beatles" — all different, real albums that just happen to also contain
// the word "Beatles". The original fix stripped one occurrence of the
// artist's name out of the raw release title and checked what was left
// for the album title; that degenerates badly whenever the artist's own
// name is a short, common word that shows up inside *other* unrelated
// album titles too — stripping "beatles" once out of "beatles beatles
// vi" (note: the full raw title has the band name twice — once as the
// artist credit, once because it's also the self-titled album) still
// leaves a second, unrelated "beatles" sitting right there to match
// against. Comparing the wanted album against the release's own *parsed*
// title (Parse's Author/Title split) instead sidesteps this, since the
// artist credit is already separated out properly rather than merely
// string-stripped once.
func TestScoreRejectsWrongAlbumWhenArtistNameIsACommonWord(t *testing.T) {
	prefs := DefaultMusicPreferences()

	for _, wrongTitle := range []string{
		"The Beatles - Beatles VI (2014 Deluxe Edition FLAC) 88",
		"The Beatles - Meet The Beatles! (2014 Deluxe Edition FLAC) 88",
		"The Beatles - With The Beatles (2014 Deluxe Edition FLAC) 88",
	} {
		c := Score(rel(wrongTitle, indexer.ProtocolUsenet, 400<<20, -1), prefs, "The Beatles", "The Beatles")
		if c.Approved {
			t.Errorf("Score(%q) approved a different album just because the artist's name recurs in it: %+v", wrongTitle, c)
		}
	}

	rightAlbum := Score(rel("The Beatles - The Beatles (White Album) (1968 Rock) [Flac 24-96]", indexer.ProtocolUsenet, 400<<20, -1), prefs, "The Beatles", "The Beatles")
	if !rightAlbum.Approved {
		t.Errorf("release actually naming the self-titled wanted album should still approve: %+v", rightAlbum.Rejections)
	}
}

// TestScoreRejectsWrongAlbumThatStartsWithTheWantedTitle is the
// regression test for a gap the *second* fix missed, found live
// immediately after deploying it, against the same real search: "The
// Beatles' Second Album", "The Beatles Ballads", and "The Beatles
// Story" all literally start with the wanted album's own title ("The
// Beatles"), which passed a TitleIsPrefixOf fallback meant for a
// genuine unbracketed edition suffix — a prefix check can't tell a
// harmless qualifier apart from a real, different album that happens to
// share the same opening words. albumRelevant no longer has that
// fallback at all.
func TestScoreRejectsWrongAlbumThatStartsWithTheWantedTitle(t *testing.T) {
	prefs := DefaultMusicPreferences()

	for _, wrongTitle := range []string{
		"The Beatles - The Beatles' Second Album (2014 Deluxe Edition FLAC) 88",
		"The Beatles - The Beatles Ballads (2002) [FLAC] [rjk]",
		"The Beatles - The Beatles Story (2014 Deluxe Edition FLAC) 88",
	} {
		c := Score(rel(wrongTitle, indexer.ProtocolUsenet, 400<<20, -1), prefs, "The Beatles", "The Beatles")
		if c.Approved {
			t.Errorf("Score(%q) approved a different album just because its title starts with the wanted album's own title: %+v", wrongTitle, c)
		}
	}
}

// TestScoreApprovesCorrectAlbumWithGluedHyphens is the regression test
// for a real bug found live, immediately after testing the fix three
// tests above: a release whose hyphens are glued straight to words
// ("The Beatles-All You Need Is Love-16BIT-WEB-FLAC-2026-OBZEN")
// correctly makes Parse decline to split it at all (see
// sanitizeReleaseTitle's own "leave a glued dash alone" rule), so
// Parsed.Title ends up being the *entire* raw string — artist name and
// codec/release-group noise included — which albumRelevant's own ratio
// check was never built to handle: diluted by all that extra noise, the
// release's own exact, correct album title scored as not relevant and
// got rejected. A genuinely different release the same real search
// surfaced, sharing every word in a different order, must still reject.
func TestScoreApprovesCorrectAlbumWithGluedHyphens(t *testing.T) {
	prefs := DefaultMusicPreferences()
	// Matches real search preferences (PreferencesFor forces this on): a
	// title with all its metadata glued together by hyphens also defeats
	// Parse's own Formats detection (strings.Fields only splits on
	// whitespace), same root cause as the album check this test exists
	// for — but that's tolerated here, scored lower rather than
	// rejected, same as any real release that simply omits the codec
	// from its name.
	prefs.AllowUnknownFormat = true

	correct := rel("The Beatles-All You Need Is Love-16BIT-WEB-FLAC-2026-OBZEN", indexer.ProtocolUsenet, 400<<20, -1)
	c := Score(correct, prefs, "The Beatles", "All You Need Is Love")
	if !c.Approved {
		t.Errorf("Score(%q) = %+v, want approved — this is the exact correct release, just with hyphens glued to words", correct.Title, c)
	}

	// Isolates albumRelevant itself (not the full Score pipeline, which
	// would also reject this on artistRelevant grounds — a pass there
	// wouldn't actually prove the album check tells these two apart):
	// shares every word with the wanted album, just in a different
	// order, and Parse likewise declines to split its own glued hyphens.
	wrongOrderTitle := "Sasha And Henry Saiz-Love Is All You Need-LNOE181D-16BIT-WEB-FLAC-2025-WAVED"
	if albumRelevant(Parse(wrongOrderTitle), "The Beatles", "All You Need Is Love") {
		t.Errorf("albumRelevant(%q) = true, want false — shares every word with the wanted album but in a different order", wrongOrderTitle)
	}
}

// TestScoreRejectsDifferentAlbumWhenWantedAlbumIsSelfTitled is the
// regression test for a real bug found live immediately after deploying
// the glued-hyphen fix above: searching Franz Ferdinand's own self-titled
// album (wantedArtist == wantedAlbum, a common shape for a debut) wrongly
// approved "Franz Ferdinand Album Discography 2004-2013", "...Always
// Ascending...", "...You Could Have It So Much Better...", "...Blood...",
// and others — every one of them a real but completely different release.
// Parse declines to split any of these (hyphens/dots glued straight to
// words), so Parsed.Title with Author == "" still contains the artist's
// own name verbatim regardless of what the actual release is; the new
// containment rescue was trivially satisfied by any release by this
// artist at all whenever the wanted album effectively *is* the wanted
// artist. The genuinely correct release must still approve.
func TestScoreRejectsDifferentAlbumWhenWantedAlbumIsSelfTitled(t *testing.T) {
	prefs := DefaultMusicPreferences()
	prefs.AllowUnknownFormat = true

	for _, wrongTitle := range []string{
		"Franz Ferdinand Album Discography 2004-2013 [FLAC]",
		"Franz Ferdinand Always Ascending [FLAC CD] 1914",
		"Franz Ferdinand-You Could Have It So Much Better-20TH ANNIVERSARY EDITION-16BIT-WEB-FLAC-2025-OBZEN",
		"Franz Ferdinand-Blood-(Advance)-2009-DV8",
	} {
		c := Score(rel(wrongTitle, indexer.ProtocolUsenet, 400<<20, -1), prefs, "Franz Ferdinand", "Franz Ferdinand")
		if c.Approved {
			t.Errorf("Score(%q) approved a different album just because the wanted album is self-titled: %+v", wrongTitle, c)
		}
	}

	// The genuinely correct release must still approve when Parse can
	// actually split it (the realistic common case — real releases of a
	// self-titled album overwhelmingly use normal "Artist - Album"
	// spacing, same as the real Franz Ferdinand release already owned
	// live: "Franz Ferdinand - Franz Ferdinand (2004) [FLAC]"). An
	// unsplit self-titled release can't be told apart from a wrong one
	// this way at all — see the doc comment above on why the rescue
	// skips this case entirely rather than guessing; that's an accepted,
	// narrow coverage loss, not something this test asserts either way.
	correct := rel("Franz Ferdinand - Franz Ferdinand (2004) [FLAC]", indexer.ProtocolUsenet, 400<<20, -1)
	c := Score(correct, prefs, "Franz Ferdinand", "Franz Ferdinand")
	if !c.Approved {
		t.Errorf("Score(%q) = %+v, want approved — this is genuinely the self-titled album", correct.Title, c)
	}
}

// TestScoreRejectsWrongNumberedSequel is the regression test for a real
// bug found live, independent of the two fixes above: wanting The
// Beatles' "Anthology 1" also auto-approved "Anthology 2", "Anthology
// 3", and "Anthology 4" — four real, separate, genuinely different
// albums. A plain Levenshtein ratio barely penalizes one differing digit
// in an otherwise-identical short title ("anthology 4" vs "anthology 1"
// scores ~0.91, nowhere near failing albumRelevantThreshold), so a
// numbered sequel needs its own explicit veto. The wanted album itself
// must still approve, and a release stating no number at all (nothing to
// conflict with) must be left to the ratio exactly as before.
func TestScoreRejectsWrongNumberedSequel(t *testing.T) {
	prefs := DefaultMusicPreferences()

	for _, wrongTitle := range []string{
		"The Beatles - Anthology 2 (2CD) [1996] FLAC",
		"The Beatles - Anthology 3 (2CD) [1996] FLAC",
		"The Beatles - Anthology 4 (2025 Rock) [Flac 24-96]",
	} {
		c := Score(rel(wrongTitle, indexer.ProtocolUsenet, 400<<20, -1), prefs, "The Beatles", "Anthology 1")
		if c.Approved {
			t.Errorf("Score(%q) approved a different numbered volume than the one wanted: %+v", wrongTitle, c)
		}
	}

	correct := rel("The Beatles - Anthology 1 (2CD) [1995] FLAC", indexer.ProtocolUsenet, 400<<20, -1)
	c := Score(correct, prefs, "The Beatles", "Anthology 1")
	if !c.Approved {
		t.Errorf("Score(%q) = %+v, want approved — this is genuinely the wanted volume", correct.Title, c)
	}

	unnumbered := rel("The Beatles - Anthology Highlights [FLAC]", indexer.ProtocolUsenet, 400<<20, -1)
	c2 := Score(unnumbered, prefs, "The Beatles", "Anthology 1")
	if c2.Approved {
		t.Errorf("Score(%q) approved a different album with no number to conflict on: %+v", unnumbered.Title, c2)
	}
}

// TestArtistRelevant covers artistRelevant's own edge cases directly:
// exact-phrase and longest-word matching, "Various Artists" always
// passing (a compilation's own file/torrent name essentially never states
// that phrase literally), and an empty wanted artist never rejecting
// (callers that don't have one to check against, e.g. tests unrelated to
// this check).
func TestArtistRelevant(t *testing.T) {
	cases := []struct {
		name         string
		releaseTitle string
		wantedArtist string
		want         bool
	}{
		{"exact phrase", "Boards of Canada - Geogaddi FLAC", "Boards of Canada", true},
		{"longest word tolerates naming variance", "Tobias Sammets Avantasia-Moonglow-FLAC", "Avantasia", true},
		{"wrong artist entirely", "Nat King Cole-Moonglow-3CD-FLAC-1995-LoKET", "Avantasia", false},
		{"various artists always passes", "Now Thats What I Call Music 42-2CD-2013-FLAC", "Various Artists", true},
		{"empty wanted artist always passes", "Anything At All", "", true},
	}
	for _, c := range cases {
		if got := artistRelevant(c.releaseTitle, c.wantedArtist); got != c.want {
			t.Errorf("%s: artistRelevant(%q, %q) = %v, want %v", c.name, c.releaseTitle, c.wantedArtist, got, c.want)
		}
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
	wav := Score(rel("Geogaddi WAV", indexer.ProtocolUsenet, 500, -1), prefs, "", "")
	if wav.Approved {
		t.Errorf("wav approved under flac/mp3 profile: %+v", wav)
	}
	flac := Score(rel("Der Geogaddi FLAC German Retail", indexer.ProtocolUsenet, 500, -1), prefs, "", "")
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
		indexer.ProtocolUsenet, 163060320, -1), prefs, "Derek and the Dominos", "")
	if !real.Approved {
		t.Errorf("real-world format-less release should approve: %+v", real)
	}
}

func TestRank(t *testing.T) {
	prefs := DefaultMusicPreferences()
	candidates := []Candidate{
		Score(rel("Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", ""),             // rejected
		Score(rel("Geogaddi MP3", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", ""),         // low
		Score(rel("Geogaddi Retail FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs, "", ""), // high
		Score(rel("Geogaddi FLAC", indexer.ProtocolTorrent, 400<<20, 5), prefs, "", ""),        // mid
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
