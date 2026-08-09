package release

import (
	"reflect"
	"testing"

	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Parsed
	}{
		{
			"Terry Pratchett - Mort (1987) Retail EPUB",
			Parsed{Author: "Terry Pratchett", Title: "Mort", Year: 1987, Formats: []string{"epub"}, Retail: true},
		},
		{
			"Terry Pratchett - Discworld 04 - Mort [EPUB] [ENG]",
			Parsed{Author: "Terry Pratchett", Title: "Mort", Formats: []string{"epub"}, Language: "english"},
		},
		{
			"Mort by Terry Pratchett EPUB",
			Parsed{Author: "Terry Pratchett", Title: "Mort", Formats: []string{"epub"}},
		},
		{
			"Terry.Pratchett.-.Mort.1987.Retail.EPUB-GROUP",
			Parsed{Author: "Terry Pratchett", Title: "Mort", Year: 1987, Formats: []string{"epub"}, Retail: true, Group: "GROUP"},
		},
		{
			"Guards! Guards! (Discworld #8) [epub/mobi/azw3]",
			// The #8 series marker parses as Volume; ebook scoring ignores it.
			Parsed{Title: "Guards! Guards!", Formats: []string{"epub", "mobi", "azw3"}, Volume: 8},
		},
		{
			"Der Mort (German) PDF",
			Parsed{Title: "Der Mort", Formats: []string{"pdf"}, Language: "german"},
		},
		{
			"Some Linux ISO x264-GRP",
			Parsed{Title: "Some Linux ISO x264-GRP"},
		},
		{
			"Terry Pratchett - Mort (Unabridged) FLAC 64kbps read by Nigel Planer",
			Parsed{Author: "Terry Pratchett", Title: "Mort", Formats: []string{"flac"},
				Bitrate: 64, Narrator: "Nigel Planer"},
		},
		{
			"Mort [Abridged] [MP3 128k] narrated by Tony Robinson",
			Parsed{Title: "Mort", Formats: []string{"mp3"}, Bitrate: 128,
				Abridged: true, Narrator: "Tony Robinson"},
		},
		{
			"Berserk v05 (2021) (Digital) [CBZ]",
			Parsed{Title: "Berserk", Year: 2021, Formats: []string{"cbz"}, Volume: 5},
		},
		{
			"The Walking Dead #12 CBR",
			Parsed{Title: "The Walking Dead", Formats: []string{"cbr"}, Volume: 12},
		},
		{
			"Berserk Volume 41 (Dark Horse) cbz",
			Parsed{Title: "Berserk", Formats: []string{"cbz"}, Volume: 41},
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

func TestScoreGeneric(t *testing.T) {
	prefs := DefaultEbookPreferences()

	epub := Score(rel("Mort Retail EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil)
	if !epub.Approved {
		t.Fatalf("epub rejected: %v", epub.Rejections)
	}
	// epub 100 + retail 25 + usenet 10
	if epub.Score != 135 {
		t.Errorf("epub score = %d, want 135", epub.Score)
	}

	pdf := Score(rel("Mort PDF", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil)
	if !pdf.Approved || pdf.Score >= epub.Score {
		t.Errorf("pdf should approve but rank below epub: %+v", pdf)
	}

	noFormat := Score(rel("Mort", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil)
	if noFormat.Approved {
		t.Error("release without a format should be rejected")
	}

	dead := Score(rel("Mort EPUB", indexer.ProtocolTorrent, 1<<20, 0), prefs, nil, nil, nil)
	if dead.Approved {
		t.Error("torrent with 0 seeders should be rejected")
	}

	seeded := Score(rel("Mort EPUB", indexer.ProtocolTorrent, 1<<20, 50), prefs, nil, nil, nil)
	if !seeded.Approved || seeded.Score != 120 { // 100 + capped 20
		t.Errorf("seeded torrent = %+v, want score 120", seeded)
	}

	tiny := Score(rel("Mort EPUB", indexer.ProtocolUsenet, 512, -1), prefs, nil, nil, nil)
	if tiny.Approved {
		t.Error("tiny file should be rejected")
	}

	german := Score(rel("Mort EPUB German", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil)
	if german.Approved {
		t.Error("non-preferred language should be rejected")
	}
}

// Comic releases routinely omit the format from the name (the real format is
// read from the files at import), so a format-less release must still
// approve — but rank below one that names a format.
func TestScoreImageMediaAllowsUnknownFormat(t *testing.T) {
	prefs := DefaultComicPreferences()

	noFormat := Score(rel("Death Note Vol. 01 (2005) (Digital)", indexer.ProtocolUsenet, 5<<20, -1), prefs, nil, nil, nil)
	if !noFormat.Approved {
		t.Fatalf("format-less comic release should approve: %v", noFormat.Rejections)
	}

	cbz := Score(rel("Death Note Vol. 01 (cbz)", indexer.ProtocolUsenet, 5<<20, -1), prefs, nil, nil, nil)
	if !cbz.Approved || cbz.Score <= noFormat.Score {
		t.Errorf("named cbz (%d) should approve and outrank unknown-format (%d)", cbz.Score, noFormat.Score)
	}

	// A stated but unwanted format is still rejected, even for image media.
	badFmt := Score(rel("Death Note Vol. 01 (mp3)", indexer.ProtocolUsenet, 5<<20, -1), prefs, nil, nil, nil)
	if badFmt.Approved {
		t.Error("an unwanted format should still be rejected for comics")
	}

	// Ebooks keep requiring a format.
	if Score(rel("Mort", indexer.ProtocolUsenet, 1<<20, -1), DefaultEbookPreferences(), nil, nil, nil).Approved {
		t.Error("ebook without a format must still reject")
	}
}

func TestScoreAgainstBook(t *testing.T) {
	prefs := DefaultEbookPreferences()
	book := &library.Book{Title: "The Colour of Magic", ReleaseDate: "1983-11-24"}
	author := &library.Author{Name: "Terry Pratchett"}

	right := Score(rel("Terry Pratchett - The Colour of Magic (1983) EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, nil)
	if !right.Approved {
		t.Fatalf("correct release rejected: %v", right.Rejections)
	}

	// Article-stripped title still matches.
	stripped := Score(rel("Terry Pratchett - Colour of Magic EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, nil)
	if !stripped.Approved {
		t.Errorf("article-stripped title rejected: %v", stripped.Rejections)
	}

	wrongBook := Score(rel("Terry Pratchett - Mort EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, nil)
	if wrongBook.Approved {
		t.Error("different book should be rejected")
	}

	wrongAuthor := Score(rel("Stephen King - The Colour of Magic EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, nil)
	if wrongAuthor.Approved {
		t.Error("missing author mention should be rejected")
	}

	// Year drift is a penalty, not a rejection.
	reprint := Score(rel("Terry Pratchett - The Colour of Magic (2009) EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, nil)
	if !reprint.Approved {
		t.Fatalf("reprint rejected: %v", reprint.Rejections)
	}
	if reprint.Score >= right.Score {
		t.Errorf("reprint (%d) should score below original-year release (%d)", reprint.Score, right.Score)
	}
}

// TestScoreFlagsPackWhenReleaseNamesAnotherBook: a release bundling the
// wanted book with another of the author's standalone titles ("Tau Zero &
// The Boat of a Million Years") must be flagged as a pack even though it
// uses none of the "Complete"/"Collection"/volume-span wording Parsed.Pack
// otherwise looks for. A release naming only the wanted book must not be.
func TestScoreFlagsPackWhenReleaseNamesAnotherBook(t *testing.T) {
	prefs := DefaultEbookPreferences()
	// The wanted book sits last in the bundled title — first would trip the
	// "immediately followed by another title" guard titleMatches uses to
	// keep a short title from matching as a prefix of a longer one (e.g.
	// "Saga" inside "Saga of the Swamp Thing"); that guard is or is not
	// tripped is exactly the same kind of "which of two titles came first"
	// distinction a real bundle presents.
	book := &library.Book{Title: "The Boat of a Million Years", ReleaseDate: "1989-01-01"}
	author := &library.Author{Name: "Poul Anderson"}
	otherTitles := []string{"Tau Zero", "The Boat of a Million Years"} // includes the wanted book itself

	bundle := Score(rel("Poul Anderson - Tau Zero & The Boat of a Million Years EPUB", indexer.ProtocolUsenet, 1<<20, -1),
		prefs, book, author, otherTitles)
	if !bundle.Approved {
		t.Fatalf("bundle release rejected: %v", bundle.Rejections)
	}
	if !bundle.Parsed.Pack {
		t.Error("release naming a second book should be flagged as a pack")
	}

	single := Score(rel("Poul Anderson - The Boat of a Million Years EPUB", indexer.ProtocolUsenet, 1<<20, -1),
		prefs, book, author, otherTitles)
	if !single.Approved {
		t.Fatalf("single-book release rejected: %v", single.Rejections)
	}
	if single.Parsed.Pack {
		t.Error("release naming only the wanted book should not be flagged as a pack")
	}
}

// TestScoreDoesNotFlagPackWhenOtherTitleIsSubstringOfWantedBook: a series
// where one book's own title is a substring of another's ("Dune" inside
// "Dune Messiah") must not have every one of the longer book's releases
// falsely flagged as a pack — titleMentioned finds "dune" as a whole word
// in "Dune Messiah" regardless, since that's just the wanted book's own name
// containing it, not a second book bundled alongside it.
func TestScoreDoesNotFlagPackWhenOtherTitleIsSubstringOfWantedBook(t *testing.T) {
	prefs := DefaultEbookPreferences()
	book := &library.Book{Title: "Dune Messiah", ReleaseDate: "1969-01-01"}
	author := &library.Author{Name: "Frank Herbert"}
	otherTitles := []string{"Dune", "Dune Messiah", "Children of Dune", "God Emperor of Dune"}

	single := Score(rel("Frank Herbert - Dune Messiah (2010) english epub", indexer.ProtocolUsenet, 1<<20, -1),
		prefs, book, author, otherTitles)
	if !single.Approved {
		t.Fatalf("release rejected: %v", single.Rejections)
	}
	if single.Parsed.Pack {
		t.Error("a single Dune Messiah release must not be flagged as a pack just because \"Dune\" is a substring of its own title")
	}

	// A genuine bundle of two distinct, non-nested titles must still flag.
	bundle := Score(rel("Frank Herbert - Dune Messiah & Children of Dune EPUB", indexer.ProtocolUsenet, 1<<20, -1),
		prefs, book, author, otherTitles)
	if !bundle.Parsed.Pack {
		t.Error("a release naming two distinct books should still be flagged as a pack")
	}
}

// TestScoreDoesNotFlagPackForDuplicateSplitEditionEntry: a messy bibliography
// can carry duplicate/split-edition rows for the same book ("Dune Messiah (1
// of 2)", "Dune Messiah (2 of 2)") whose title, once TitleKeys strips the
// trailing parenthetical, is identical to the real book's own title — that
// must not make every single release of the real book look like it also
// bundles these duplicates. A bare-author-name row (a stray anthology/bio
// credit off provider metadata) must likewise never count, since the
// author's name appears in essentially every release.
func TestScoreDoesNotFlagPackForDuplicateSplitEditionEntry(t *testing.T) {
	prefs := DefaultEbookPreferences()
	book := &library.Book{Title: "Dune Messiah", ReleaseDate: "1969-01-01"}
	author := &library.Author{Name: "Frank Herbert"}
	otherTitles := []string{
		"Dune", "Dune Messiah", "Dune Messiah (1 of 2)", "Dune Messiah (2 of 2)",
		"Frank Herbert", // bare author-name garbage row
	}

	for _, title := range []string{
		"Frank Herbert - Dune Messiah (2010) english epub",
		"Herbert, Frank - Dune Messiah (1965) english epub",
	} {
		c := Score(rel(title, indexer.ProtocolUsenet, 1<<20, -1), prefs, book, author, otherTitles)
		if !c.Approved {
			t.Fatalf("%q: release rejected: %v", title, c.Rejections)
		}
		if c.Parsed.Pack {
			t.Errorf("%q: flagged as a pack — should only match its own duplicate/split-edition rows and the author's own name", title)
		}
	}
}

// TestScoreAuthorFallsBackToKeywords: some scraped native sources' series/
// collection posts often omit the author from the title but carry it in a
// separate tag list (Release.Keywords) instead — that must satisfy the
// author check just like a title mention would, but a book-title mismatch
// must still reject even when Keywords happens to contain the author.
func TestScoreAuthorFallsBackToKeywords(t *testing.T) {
	prefs := DefaultComicPreferences() // AllowUnknownFormat: true — this release states none
	book := &library.Book{Title: "Dune Messiah"}
	author := &library.Author{Name: "Frank Herbert"}

	bare := indexer.Release{
		Indexer: "abb", Protocol: indexer.ProtocolTorrent, Title: "Dune Messiah",
		Keywords:    "Dune Frank Herbert",
		DownloadURL: "https://abb.example/dune-messiah/",
		Size:        200 << 20, Seeders: -1, Peers: -1,
	}
	c := Score(bare, prefs, book, author, nil)
	if !c.Approved {
		t.Fatalf("author-in-keywords release rejected: %v", c.Rejections)
	}

	noKeywordMatch := bare
	noKeywordMatch.Keywords = "Dune Science Fiction"
	c = Score(noKeywordMatch, prefs, book, author, nil)
	if c.Approved {
		t.Error("release naming neither the author in title nor keywords should still reject")
	}

	wrongBook := bare
	wrongBook.Title = "Dune"
	c = Score(wrongBook, prefs, book, author, nil)
	if c.Approved {
		t.Error("a different book must still reject even when keywords name the right author")
	}
}

func TestPreferencesFromProfile(t *testing.T) {
	prefs := PreferencesFromProfile(library.QualityProfile{
		Formats:     []string{"azw3", "epub"},
		Language:    "german",
		RetailBonus: 40,
		MinSize:     100,
		MaxSize:     1000,
	})
	if prefs.FormatScores["azw3"] != 100 || prefs.FormatScores["epub"] != 80 {
		t.Errorf("format scores = %v", prefs.FormatScores)
	}
	if _, ok := prefs.FormatScores["pdf"]; ok {
		t.Error("unlisted format should be absent (rejected)")
	}

	// An epub-only German profile rejects English pdf, prefers azw3.
	pdf := Score(rel("Mort PDF", indexer.ProtocolUsenet, 500, -1), prefs, nil, nil, nil)
	if pdf.Approved {
		t.Errorf("pdf approved under azw3/epub profile: %+v", pdf)
	}
	azw3 := Score(rel("Der Mort AZW3 German Retail", indexer.ProtocolUsenet, 500, -1), prefs, nil, nil, nil)
	if !azw3.Approved || azw3.Score != 150 { // 100 + retail 40 + usenet 10
		t.Errorf("azw3 = %+v, want approved score 150", azw3)
	}

	// Long format lists floor at 20.
	many := PreferencesFromProfile(library.QualityProfile{
		Formats: []string{"epub", "azw3", "mobi", "pdf", "cbz", "cbr"},
	})
	if many.FormatScores["cbz"] != 20 || many.FormatScores["cbr"] != 20 {
		t.Errorf("floored scores = %v", many.FormatScores)
	}
}

func TestScoreMusic(t *testing.T) {
	prefs := DefaultMusicPreferences()

	flac := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 400<<20, -1), prefs, nil, nil, nil)
	if !flac.Approved {
		t.Errorf("flac should approve: %v", flac.Rejections)
	}
	mp3 := Score(rel("Boards of Canada - Geogaddi MP3", indexer.ProtocolUsenet, 100<<20, -1), prefs, nil, nil, nil)
	if !mp3.Approved {
		t.Errorf("mp3 should approve: %v", mp3.Rejections)
	}
	if mp3.Score >= flac.Score {
		t.Errorf("mp3 (%d) should rank below flac (%d)", mp3.Score, flac.Score)
	}
	epub := Score(rel("Boards of Canada - Geogaddi EPUB", indexer.ProtocolUsenet, 400<<20, -1), prefs, nil, nil, nil)
	if epub.Approved {
		t.Error("ebook format should be rejected under music prefs")
	}
	noFmt := Score(rel("Boards of Canada - Geogaddi", indexer.ProtocolUsenet, 400<<20, -1), prefs, nil, nil, nil)
	if noFmt.Approved {
		t.Error("music release names should be required to state a recognized format")
	}
	tiny := Score(rel("Boards of Canada - Geogaddi FLAC", indexer.ProtocolUsenet, 1<<10, -1), prefs, nil, nil, nil)
	if tiny.Approved {
		t.Error("1 KiB flac release should be rejected as suspiciously small")
	}
}

func TestScoreVolume(t *testing.T) {
	prefs := DefaultComicPreferences()

	right := ScoreVolume(rel("Berserk v05 (Digital) CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if !right.Approved {
		t.Fatalf("right volume rejected: %v", right.Rejections)
	}
	wrongVol := ScoreVolume(rel("Berserk v06 CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if wrongVol.Approved {
		t.Error("wrong volume approved")
	}
	noVol := ScoreVolume(rel("Berserk Complete CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if noVol.Approved {
		t.Error("volume-less release approved")
	}
	wrongSeries := ScoreVolume(rel("One Piece v05 CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if wrongSeries.Approved {
		t.Error("wrong series approved")
	}
	// A short series title must not match a longer, different title that only
	// starts with it ("Saga" vs "Saga of the Swamp Thing").
	prefixSeries := ScoreVolume(rel("Saga of the Swamp Thing v01 CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Saga", 1)
	if prefixSeries.Approved {
		t.Errorf("longer prefix-title series wrongly approved: %+v", prefixSeries.Rejections)
	}
	// …but a leading publisher/scanlator tag before the title still matches.
	tagged := ScoreVolume(rel("Berserk Dark Horse v05 CBZ", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if !tagged.Approved {
		t.Errorf("tagged release wrongly rejected: %v", tagged.Rejections)
	}
	epubUnderComic := ScoreVolume(rel("Berserk v05 EPUB", indexer.ProtocolUsenet, 50<<20, -1), prefs, "Berserk", 5)
	if epubUnderComic.Approved {
		t.Error("epub approved under comic prefs")
	}
}

func TestRank(t *testing.T) {
	prefs := DefaultEbookPreferences()
	candidates := []Candidate{
		Score(rel("Mort", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil),             // rejected
		Score(rel("Mort PDF", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil),         // low
		Score(rel("Mort Retail EPUB", indexer.ProtocolUsenet, 1<<20, -1), prefs, nil, nil, nil), // high
		Score(rel("Mort EPUB", indexer.ProtocolTorrent, 1<<20, 5), prefs, nil, nil, nil),        // mid
	}
	Rank(candidates)
	if !candidates[0].Approved || candidates[0].Score != 135 {
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

func TestParseVolumeRange(t *testing.T) {
	cases := []struct {
		title      string
		start, end float64
		pack       bool
	}{
		{"Berserk v01-v41 (Digital) (CBZ)", 1, 41, false},
		{"Saga Vol. 1-10 (2023)", 1, 10, false},
		{"Monster c001-c162 [Complete]", 1, 162, true},
		{"Y The Last Man #1-60", 1, 60, false},
		{"One Piece Complete Collection", 0, 0, true},
		{"Berserk v05 (Digital)", 5, 0, false},
		{"World History 2020-2021 EPUB", 0, 0, false}, // year span, not volumes
	}
	for _, c := range cases {
		p := Parse(c.title)
		if p.Volume != c.start || p.VolumeEnd != c.end || p.Pack != c.pack {
			t.Errorf("%q = vol %v end %v pack %v, want %v %v %v",
				c.title, p.Volume, p.VolumeEnd, p.Pack, c.start, c.end, c.pack)
		}
	}
}

func TestScoreSeriesPack(t *testing.T) {
	prefs := DefaultComicPreferences()

	full := ScoreSeriesPack(rel("Berserk v01-v41 (Digital) (CBZ)", indexer.ProtocolTorrent, 10<<30, 12), prefs, "Berserk", 41)
	if !full.Approved {
		t.Fatalf("full range pack rejected: %v", full.Rejections)
	}

	partial := ScoreSeriesPack(rel("Berserk v01-v20 CBZ", indexer.ProtocolTorrent, 5<<30, 8), prefs, "Berserk", 41)
	if !partial.Approved {
		t.Fatalf("partial pack rejected: %v", partial.Rejections)
	}
	if partial.Score >= full.Score {
		t.Errorf("partial (%d) should rank below full (%d)", partial.Score, full.Score)
	}

	bare := ScoreSeriesPack(rel("Berserk (Digital) (CBZ)", indexer.ProtocolTorrent, 12<<30, 20), prefs, "Berserk", 41)
	if !bare.Approved {
		t.Fatalf("bare series release rejected: %v", bare.Rejections)
	}
	if bare.Score >= full.Score {
		t.Errorf("bare (%d) should rank below explicit range (%d)", bare.Score, full.Score)
	}

	single := ScoreSeriesPack(rel("Berserk v05 (Digital) CBZ", indexer.ProtocolTorrent, 300<<20, 30), prefs, "Berserk", 41)
	if single.Approved {
		t.Error("single-volume release should be rejected from pack search")
	}

	wrong := ScoreSeriesPack(rel("Vagabond v01-v37 CBZ", indexer.ProtocolTorrent, 9<<30, 15), prefs, "Berserk", 41)
	if wrong.Approved {
		t.Error("wrong series should be rejected")
	}
}
