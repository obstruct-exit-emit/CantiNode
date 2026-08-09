package release

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/scanner"
)

// titleStopwords are words that, right after a matched title, mean the release
// is actually a longer, different title ("Saga" vs "Saga of the Swamp Thing")
// rather than a scene/publisher tag ("Berserk Dark Horse v05") or metadata
// ("Berserk v05"). A tag or metadata token never starts with one of these.
var titleStopwords = map[string]bool{
	"of": true, "the": true, "a": true, "an": true, "and": true,
	"or": true, "in": true, "to": true, "vs": true, "versus": true, "for": true,
}

// authorMatches reports whether a release mentions the author: either the
// full normalized name as a contiguous run, or — for "Last, First" style
// sources like Libgen where the words are present but reordered — every
// significant (2+ char) word of the name somewhere in the release. Single-char
// initials are ignored so "Kevin J. Anderson" matches "Kevin, J. Anderson".
func authorMatches(relNorm, authorNorm string) bool {
	if strings.Contains(relNorm, authorNorm) {
		return true
	}
	have := map[string]bool{}
	for _, w := range strings.Fields(relNorm) {
		have[w] = true
	}
	matched := 0
	for _, w := range strings.Fields(authorNorm) {
		if len(w) < 2 {
			continue
		}
		if !have[w] {
			return false
		}
		matched++
	}
	return matched > 0
}

// titleMatches reports whether any normalized title key appears in the
// normalized release title as a whole-word run not immediately continued by a
// stopword. Matching whole words (not substrings) avoids "Saga" hitting "Saga
// of the Swamp Thing", while still allowing a leading group/scanlator tag.
func titleMatches(relNorm string, keys []string) bool {
	relWords := strings.Fields(relNorm)
	for _, key := range keys {
		kw := strings.Fields(key)
		if len(kw) == 0 {
			continue
		}
		for i := 0; i+len(kw) <= len(relWords); i++ {
			if slices.Equal(relWords[i:i+len(kw)], kw) {
				next := i + len(kw)
				if next >= len(relWords) || !titleStopwords[relWords[next]] {
					return true
				}
			}
		}
	}
	return false
}

// titleMentioned reports whether any normalized title key appears in the
// release title as a whole-word run — unlike titleMatches, a match right
// before another word (a stopword or otherwise) still counts. Pack detection
// wants exactly the case titleMatches' stopword guard excludes: that's the
// boundary between two concatenated titles in a bundle ("Tau Zero & THE Boat
// of a Million Years"), not a false-positive risk the way it is for the
// primary book-title check.
func titleMentioned(relNorm string, keys []string) bool {
	relWords := strings.Fields(relNorm)
	for _, key := range keys {
		kw := strings.Fields(key)
		if len(kw) == 0 {
			continue
		}
		for i := 0; i+len(kw) <= len(relWords); i++ {
			if slices.Equal(relWords[i:i+len(kw)], kw) {
				return true
			}
		}
	}
	return false
}

// titleCoveredByAny reports whether every one of keys is itself equal to, or
// a substring of, some key in of — meaning keys names nothing beyond what of
// already covers ("dune" contributes nothing next to "dune messiah"; a
// duplicate row's stripped title contributes nothing when it lands on
// exactly one of of's own keys).
func titleCoveredByAny(keys, of []string) bool {
	for _, k := range keys {
		if k == "" {
			continue
		}
		covered := false
		for _, o := range of {
			if o == k || strings.Contains(o, k) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return len(keys) > 0
}

// independentKeys returns the keys in tKeys not covered (equal to, or a
// substring of) any key in bookKeys — the parts of an other title that
// genuinely name something beyond the wanted book's own title.
func independentKeys(tKeys, bookKeys []string) []string {
	var out []string
	for _, k := range tKeys {
		if k == "" {
			continue
		}
		covered := false
		for _, bk := range bookKeys {
			if bk == k || strings.Contains(bk, k) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, k)
		}
	}
	return out
}

// Preferences drive scoring. Each media type's default quality profile
// produces these (PreferencesFor); the Default*Preferences constructors are
// the built-in fallbacks when no profile exists.
type Preferences struct {
	// FormatScores ranks acceptable formats; formats absent from the map
	// are rejected.
	FormatScores map[string]int
	RetailBonus  int
	// Language "" accepts anything; otherwise releases stating a different
	// language are rejected (unstated passes).
	Language string
	MinSize  int64
	MaxSize  int64
	// MinFormatScore, when > 0, rejects formats scoring at or below it —
	// used by upgrade searches so only genuinely better formats approve.
	MinFormatScore int
	// RejectAbridged drops releases that state they are abridged
	// (audiobook profiles default to true).
	RejectAbridged bool
	// AllowUnknownFormat accepts releases whose name states no format instead
	// of rejecting them — manga/comic/magazine scene names routinely omit it
	// (the real format is read from the files at import). Ebooks/audiobooks
	// keep requiring a recognized format.
	AllowUnknownFormat bool
}

// unknownFormatScore is the baseline a format-less release gets when
// AllowUnknownFormat is set — positive so it can approve, but below any named
// format so a release that does state cbz/cbr/pdf outranks it.
const unknownFormatScore = 30

// PreferencesFor resolves the active scoring rules for a media type: its
// default quality profile when one exists, built-in defaults otherwise.
func PreferencesFor(store *library.Store, mediaType string) Preferences {
	if p, err := store.DefaultProfile(mediaType); err == nil {
		prefs := PreferencesFromProfile(*p)
		prefs.RejectAbridged = mediaType == "audiobook"
		prefs.AllowUnknownFormat = formatOptional(mediaType)
		return prefs
	}
	switch mediaType {
	case "audiobook":
		return DefaultAudiobookPreferences()
	case "manga":
		return DefaultMangaPreferences()
	case "comic":
		return DefaultComicPreferences()
	case "magazine":
		return DefaultMagazinePreferences()
	}
	return DefaultEbookPreferences()
}

// isImageMedia reports whether a media type ships as image archives (cbz/cbr)
// or periodicals whose release names routinely omit the format.
func isImageMedia(mediaType string) bool {
	return mediaType == "manga" || mediaType == "comic" || mediaType == "magazine"
}

// formatOptional reports media types whose release names routinely omit the
// file format, so a format-less name shouldn't be rejected — image media
// (cbz/cbr) plus audiobooks (bitrate/narrator instead of m4b/mp3). The importer
// verifies the real format from the downloaded files.
func formatOptional(mediaType string) bool {
	return isImageMedia(mediaType) || mediaType == "audiobook"
}

// DefaultMagazinePreferences scores pdf first — periodicals ship as pdf.
func DefaultMagazinePreferences() Preferences {
	return Preferences{
		FormatScores:       map[string]int{"pdf": 100, "epub": 70, "cbz": 50},
		Language:           "english",
		MinSize:            1 << 20,
		MaxSize:            1 << 30,
		AllowUnknownFormat: true,
	}
}

// DefaultMangaPreferences prefer lossless archives; scanlation sizes run
// from a few MB per volume to a GB+ for omnibus scans.
func DefaultMangaPreferences() Preferences {
	return Preferences{
		FormatScores:       map[string]int{"cbz": 100, "cbr": 80, "epub": 60, "pdf": 40},
		Language:           "english",
		MinSize:            2 << 20,
		MaxSize:            2 << 30,
		AllowUnknownFormat: true,
	}
}

// DefaultComicPreferences mirror manga but drop epub (comics don't ship it).
func DefaultComicPreferences() Preferences {
	return Preferences{
		FormatScores:       map[string]int{"cbz": 100, "cbr": 80, "pdf": 40},
		Language:           "english",
		MinSize:            2 << 20,
		MaxSize:            2 << 30,
		AllowUnknownFormat: true,
	}
}

// DefaultAudiobookPreferences prefers single-file m4b (Audiobookshelf's
// favorite), then space-efficient formats; abridged versions are rejected.
// Format is optional: audiobook release names routinely carry the bitrate or
// narrator instead of m4b/mp3 (the importer reads the real format from files).
func DefaultAudiobookPreferences() Preferences {
	return Preferences{
		FormatScores:       map[string]int{"m4b": 100, "m4a": 85, "opus": 75, "mp3": 70, "flac": 55},
		RetailBonus:        10,
		Language:           "english",
		MinSize:            5 << 20, // 5 MiB — shorter than a short story
		MaxSize:            4 << 30, // 4 GiB — beyond even long unabridged epics
		RejectAbridged:     true,
		AllowUnknownFormat: true,
	}
}

func DefaultEbookPreferences() Preferences {
	return Preferences{
		FormatScores: map[string]int{"epub": 100, "azw3": 80, "mobi": 60, "pdf": 30},
		RetailBonus:  25,
		Language:     "english",
		MinSize:      20 << 10,  // 20 KiB — anything smaller isn't a book
		MaxSize:      500 << 20, // 500 MiB — anything bigger isn't an ebook
	}
}

// PreferencesFromProfile converts a quality profile into scoring
// preferences. Format scores derive from list order: best 100, then
// descending in steps of 20 (floored at 20).
func PreferencesFromProfile(p library.QualityProfile) Preferences {
	prefs := Preferences{
		FormatScores: make(map[string]int, len(p.Formats)),
		RetailBonus:  p.RetailBonus,
		Language:     p.Language,
		MinSize:      p.MinSize,
		MaxSize:      p.MaxSize,
	}
	for i, f := range p.Formats {
		score := 100 - 20*i
		if score < 20 {
			score = 20
		}
		prefs.FormatScores[f] = score
	}
	return prefs
}

// Candidate is a release with its parse, score, and verdict. Release fields
// stay flat in JSON via embedding.
type Candidate struct {
	indexer.Release
	Parsed     Parsed   `json:"parsed"`
	Score      int      `json:"score"`
	Approved   bool     `json:"approved"`
	Rejections []string `json:"rejections,omitempty"`
}

// Score evaluates one release. book and author are optional: without them
// only generic checks run (format, size, health); with them the release must
// actually be the wanted book. otherTitles is the author's other book
// titles, used only to flag a release that bundles more than one of them as
// a pack — pass nil when unavailable (the "Complete"/"Collection"/volume-span
// title signals in Parsed.Pack still apply either way).
func Score(rel indexer.Release, prefs Preferences, book *library.Book, author *library.Author, otherTitles []string) Candidate {
	c := Candidate{Release: rel, Parsed: Parse(rel.Title)}

	// Spam guard: a release whose name states an executable/installer extension
	// is malware masquerading as the book — reject before it can be grabbed.
	// (Most spam hides a clean name and is only caught at import; this catches
	// the ones that name it outright.)
	if scanner.NamesExecutable(rel.Title) {
		c.reject("release names an executable — likely spam")
	}

	// Format: best recognized format wins; none recognized is fatal.
	best := -1
	for _, f := range c.Parsed.Formats {
		if s, ok := prefs.FormatScores[f]; ok && s > best {
			best = s
		}
	}
	switch {
	case len(c.Parsed.Formats) == 0 && prefs.AllowUnknownFormat:
		// No format stated, but manga/comic/magazine names often omit it; the
		// importer verifies the actual file's format. Give a low baseline.
		c.Score += unknownFormatScore
	case len(c.Parsed.Formats) == 0:
		c.reject("no recognized ebook format in release name")
	case best < 0:
		c.reject(fmt.Sprintf("format %s not wanted", strings.Join(c.Parsed.Formats, "/")))
	case prefs.MinFormatScore > 0 && best <= prefs.MinFormatScore:
		c.reject("not an upgrade over the owned format")
	default:
		c.Score += best
	}

	if c.Parsed.Retail {
		c.Score += prefs.RetailBonus
	}

	if prefs.Language != "" && c.Parsed.Language != "" && c.Parsed.Language != prefs.Language {
		c.reject("language " + c.Parsed.Language + " not wanted")
	}

	if prefs.RejectAbridged && c.Parsed.Abridged {
		c.reject("abridged")
	}

	if rel.Size > 0 {
		if rel.Size < prefs.MinSize {
			c.reject("suspiciously small file")
		}
		if rel.Size > prefs.MaxSize {
			c.reject("too large for an ebook")
		}
	}

	// Protocol health: dead torrents are useless; live ones get a bounded
	// seeder bonus, usenet a flat availability bonus.
	if rel.Protocol == indexer.ProtocolTorrent {
		if rel.Seeders == 0 {
			c.reject("no seeders")
		} else if rel.Seeders > 0 {
			c.Score += min(rel.Seeders, 20)
		}
	} else {
		c.Score += 10
	}

	// A release without a download link can never be grabbed — surface why
	// instead of failing at the grab (e.g. a membership-gated direct source
	// searched without its key).
	if rel.DownloadURL == "" {
		c.reject("no download link (the source may need a membership/API key)")
	}

	if book != nil {
		c.matchBook(book, author, otherTitles)
	}

	c.Approved = len(c.Rejections) == 0
	return c
}

// ScoreVolume evaluates a release against a wanted manga volume / comic
// issue: generic checks plus the series title and the exact volume number.
func ScoreVolume(rel indexer.Release, prefs Preferences, seriesTitle string, number float64) Candidate {
	c := Score(rel, prefs, nil, nil, nil)

	relNorm := scanner.Normalize(rel.Title)
	if !titleMatches(relNorm, scanner.TitleKeys(seriesTitle)) {
		c.reject("does not contain the series title")
	}

	switch {
	case c.Parsed.Volume == 0:
		c.reject("no volume/issue number in release name")
	case c.Parsed.Volume != number:
		c.reject(fmt.Sprintf("volume %v, wanted %v", c.Parsed.Volume, number))
	}

	c.Approved = len(c.Rejections) == 0
	return c
}

// ScoreSeriesPack evaluates a release as a whole-series (or multi-volume)
// pack: generic checks, the series title, and pack-ness — a volume range
// ("v01-v12"), a completeness word ("Complete", "Collection"), or a bare
// series-title release with no volume number at all (the common shape for
// manga packs). Single-volume releases are rejected — they belong to the
// per-volume flow. maxWanted is the highest missing position; a range that
// reaches it earns a bonus, one that falls short is kept but penalized.
func ScoreSeriesPack(rel indexer.Release, prefs Preferences, seriesTitle string, maxWanted float64) Candidate {
	// A pack is dozens of volumes in one release — the per-item size cap
	// doesn't apply. 100 GiB still guards against nonsense.
	prefs.MaxSize = 100 << 30
	c := Score(rel, prefs, nil, nil, nil)

	relNorm := scanner.Normalize(rel.Title)
	if !titleMatches(relNorm, scanner.TitleKeys(seriesTitle)) {
		c.reject("does not contain the series title")
	}

	switch {
	case c.Parsed.VolumeEnd > 0:
		// An explicit span is the clearest pack signal.
		c.Score += 30
		if maxWanted > 0 && c.Parsed.VolumeEnd >= maxWanted {
			c.Score += 20 // covers everything missing
		} else if maxWanted > 0 {
			c.Score -= 10 // partial pack: usable, ranked below full ones
		}
	case c.Parsed.Pack:
		c.Score += 25
	case c.Parsed.Volume == 0:
		// A bare series-title release usually IS the series. Accepted, but
		// outranked by anything that says so explicitly.
	default:
		c.reject(fmt.Sprintf("single volume %v — use the per-volume search", c.Parsed.Volume))
	}

	c.Approved = len(c.Rejections) == 0
	return c
}

// ScoreMagazine evaluates a release for a magazine: generic checks, the
// magazine's title, and an issue identifier (date or number) that isn't
// already owned. The identifier is returned so the caller can materialize
// the issue on grab.
func ScoreMagazine(rel indexer.Release, prefs Preferences, title string, owned map[string]bool) (Candidate, string) {
	c := Score(rel, prefs, nil, nil, nil)

	relNorm := scanner.Normalize(rel.Title)
	if !titleMatches(relNorm, scanner.TitleKeys(title)) {
		c.reject("does not contain the magazine title")
	}

	identifier := scanner.IssueIdentifier(rel.Title)
	if identifier == "" {
		c.reject("no issue date or number in release name")
	} else if owned[identifier] {
		c.reject("issue " + identifier + " already owned")
	}

	c.Approved = len(c.Rejections) == 0
	return c, identifier
}

// matchBook rejects releases that don't look like the wanted book.
func (c *Candidate) matchBook(book *library.Book, author *library.Author, otherTitles []string) {
	relNorm := scanner.Normalize(c.Release.Title)

	if !titleMatches(relNorm, scanner.TitleKeys(book.Title)) {
		c.reject("does not contain the book title")
	}

	// Pack detection beyond the "Complete"/"Collection"/volume-span title
	// signals already in Parsed.Pack: a release naming the wanted book
	// alongside another of the author's standalone titles ("Tau Zero & The
	// Boat of a Million Years") bundles two books without using any of those
	// words at all.
	if !c.Parsed.Pack {
		bookKeys := scanner.TitleKeys(book.Title)
		var authorNorm string
		if author != nil {
			authorNorm = scanner.Normalize(author.Name)
		}
		for _, t := range otherTitles {
			if t == "" || strings.EqualFold(t, book.Title) {
				continue
			}
			// A book title that's nothing but the author's own name (a
			// stray anthology/bio credit in a messy bibliography) proves
			// nothing — the author's name appears in essentially every
			// release for them.
			if authorNorm != "" && titleCoveredByAny(scanner.TitleKeys(t), []string{authorNorm}) {
				continue
			}
			// Keep only the keys this other title carries that the wanted
			// book's own title doesn't already reach on its own — a
			// duplicate or split-edition row for the *same* book ("Dune
			// Messiah (1 of 2)") strips down to exactly the wanted book's
			// title and must not count as a second book just because that
			// stripped form, unsurprisingly, also appears in every one of
			// the wanted book's own releases.
			independent := independentKeys(scanner.TitleKeys(t), bookKeys)
			if len(independent) == 0 {
				continue
			}
			if titleMentioned(relNorm, independent) {
				c.Parsed.Pack = true
				break
			}
		}
	}

	if author != nil {
		authorNorm := scanner.Normalize(author.Name)
		// Some sources (AudioBook Bay) omit the author from the title on a
		// series/collection post but carry it in a separate tag list instead
		// — fall back to that rather than rejecting a release the source
		// itself associates with the right author. Never used for the
		// book-title check above: a stray tag match there would be too easy
		// to fool.
		matchText := relNorm
		if c.Release.Keywords != "" {
			matchText = relNorm + " " + scanner.Normalize(c.Release.Keywords)
		}
		if authorNorm != "" && !authorMatches(matchText, authorNorm) {
			c.reject("does not mention the author")
		}
	}

	// A stated year far from the book's original release is suspicious but
	// not fatal (editions differ): small penalty.
	if c.Parsed.Year > 0 && len(book.ReleaseDate) >= 4 {
		if bookYear, err := parseYear(book.ReleaseDate[:4]); err == nil {
			diff := c.Parsed.Year - bookYear
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 {
				c.Score -= 5
			}
		}
	}
}

func parseYear(s string) (int, error) {
	var y int
	_, err := fmt.Sscanf(s, "%d", &y)
	return y, err
}

func (c *Candidate) reject(reason string) {
	c.Rejections = append(c.Rejections, reason)
}

// Rank sorts candidates in place: approved before rejected, then by score.
func Rank(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Approved != candidates[j].Approved {
			return candidates[i].Approved
		}
		return candidates[i].Score > candidates[j].Score
	})
}
