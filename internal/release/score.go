package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
	"github.com/cantinode/cantinode/internal/relname"
)

// artistRelevant reports whether releaseTitle plausibly names wantedArtist
// — the check that used to be entirely missing from Score: nothing
// compared a candidate's own title against who was actually being
// searched for, so a release that merely shared a word with the wanted
// album (e.g. Nat King Cole's own "Moonglow" surfacing for a search for
// Avantasia's "Moonglow") could score and auto-grab purely on format/size/
// health merits. "Various Artists" always passes — a compilation's own
// file/torrent name essentially never states that phrase literally, so
// requiring it would reject good VA results instead of catching bad ones.
// An exact-phrase match handles the common case; a fallback to just the
// artist name's longest word tolerates realistic naming variance
// ("Tobias Sammets Avantasia" for artist "Avantasia") without needing a
// full fuzzy-matching pass for what's ultimately a spam/wrong-item guard.
func artistRelevant(releaseTitle, wantedArtist string) bool {
	wantedArtist = strings.TrimSpace(wantedArtist)
	if wantedArtist == "" || strings.EqualFold(wantedArtist, "Various Artists") {
		return true
	}
	normTitle := relname.Normalize(releaseTitle)
	normArtist := relname.Normalize(wantedArtist)
	if normArtist == "" || strings.Contains(normTitle, normArtist) {
		return true
	}
	longest := ""
	for _, w := range strings.Fields(normArtist) {
		if len(w) > len(longest) {
			longest = w
		}
	}
	return longest != "" && strings.Contains(normTitle, longest)
}

// albumRelevantThreshold is deliberately stricter than slotTrack's own
// real title-match threshold (0.6, per folder_match.go) — found live on
// this constant's first value (0.5): "Meet The Beatles!" and "With The
// Beatles" both score ~0.58-0.65 against wanted album "The Beatles",
// since the wanted title sits intact as a literal substring of a longer,
// genuinely different real album title — a plain Levenshtein ratio
// doesn't penalize that kind of containment enough. 0.75 rejects both
// while still passing a real edition variant that merely adds a short
// bracketed qualifier Parse didn't strip (TitleIsPrefixOf below is the
// real safety net for that case regardless of where the ratio lands —
// see its own call site).
const albumRelevantThreshold = 0.75

// albumRelevant is artistRelevant's own missing other half, found live: a
// search only ever verified a candidate's title plausibly named the right
// ARTIST, never the right ALBUM — any release by the correct artist could
// score and auto-grab on format/size/health merits alone, regardless of
// which actual album it was. Confirmed live against a real wrong-album
// grab: searching for Franz Ferdinand's self-titled album "Franz
// Ferdinand" surfaced (and would have auto-approved) their unrelated
// "The Human Fear" instead — nothing ever checked the album title at all.
//
// Takes the release's own *parsed* title (Parse's own Author/Title split,
// already computed by Score before this runs) rather than the raw release
// string. An earlier version compared against the raw title directly,
// stripping one occurrence of the artist's name first — that handled the
// Franz Ferdinand case, but broke down hard on any artist whose name is
// itself a short, common word that shows up inside *other* genuinely
// different album titles too: confirmed live searching for The Beatles'
// self-titled "The Beatles" (White Album), where "Beatles VI", "Meet The
// Beatles!", and "With The Beatles" all still scored as a match, because
// stripping "beatles" once out of e.g. "beatles beatles vi" still leaves
// a second, unrelated "beatles" sitting right there. Comparing against
// the already-parsed title sidesteps this entirely — Parse's own
// Author/Title split (the same "Artist - Album" dash convention
// essentially every real release name follows) has already separated the
// artist credit out, so "Beatles VI" is compared as just "Beatles VI",
// not as a string the artist's own name is still embedded in.
//
// relname.TitleSimilarity, not a raw substring/longest-word check — a
// release name's own bracketed year/format annotations are already
// stripped by Parse, but a genuine edition difference ("The Beatles" vs
// "The Beatles (Mono Mix)" if that qualifier wasn't bracketed) still
// needs the same tolerance slotTrack's own title check already gives an
// embedded tag, via TitleIsPrefixOf alongside the similarity ratio.
func albumRelevant(parsedTitle, wantedAlbum string) bool {
	wantedAlbum = strings.TrimSpace(wantedAlbum)
	if wantedAlbum == "" || parsedTitle == "" {
		return true
	}
	if relname.TitleSimilarity(parsedTitle, wantedAlbum) >= albumRelevantThreshold {
		return true
	}
	return relname.TitleIsPrefixOf(parsedTitle, wantedAlbum)
}

// Preferences drive scoring. The media type's default quality profile
// produces these (PreferencesFor); DefaultMusicPreferences is the built-in
// fallback when no profile exists.
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
	// AllowUnknownFormat accepts releases whose name states no format
	// instead of rejecting them.
	AllowUnknownFormat bool
}

// unknownFormatScore is the baseline a format-less release gets when
// AllowUnknownFormat is set — positive so it can approve, but below any named
// format.
const unknownFormatScore = 30

// PreferencesFor resolves the active scoring rules for a media type: its
// default quality profile when one exists, built-in defaults otherwise.
// AllowUnknownFormat is forced on regardless of source: real-world music
// release titles routinely name the source rather than the codec ("SHM-CD",
// "24-96 hdtracks", "4CD Box") — confirmed against a live Prowlarr search,
// where every result omitted flac/mp3/etc. outright — so treating a
// format-less title as an automatic rejection would reject nearly
// everything real indexers actually return.
func PreferencesFor(store *library.Store, mediaType string) Preferences {
	var prefs Preferences
	if p, err := store.DefaultProfile(mediaType); err == nil {
		prefs = PreferencesFromProfile(*p)
	} else {
		prefs = DefaultMusicPreferences()
	}
	prefs.AllowUnknownFormat = true
	return prefs
}

// DefaultMusicPreferences prefers lossless FLAC, then space-efficient lossy
// formats; sizes span a single short track up to a large lossless
// multi-disc discography pack.
func DefaultMusicPreferences() Preferences {
	return Preferences{
		FormatScores: map[string]int{"flac": 100, "wav": 90, "mp3": 70, "m4a": 65, "opus": 60},
		MinSize:      1 << 20, // 1 MiB — shorter than any real track
		MaxSize:      4 << 30, // 4 GiB — a large lossless multi-disc album/discography
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

// Score evaluates one release against generic checks: format, size, health,
// and — since wantedArtist is non-empty — whether the release is even
// plausibly the right artist at all.
func Score(rel indexer.Release, prefs Preferences, wantedArtist, wantedAlbum string) Candidate {
	c := Candidate{Release: rel, Parsed: Parse(rel.Title)}

	// Spam guard: a release whose name states an executable/installer extension
	// is malware masquerading as the real content — reject before it can be
	// grabbed. (Most spam hides a clean name and is only caught at import;
	// this catches the ones that name it outright.)
	if relname.NamesExecutable(rel.Title) {
		c.reject("release names an executable — likely spam")
	}

	if !artistRelevant(rel.Title, wantedArtist) {
		c.reject(fmt.Sprintf("release title doesn't appear to be %s", wantedArtist))
	}
	if !albumRelevant(c.Parsed.Title, wantedAlbum) {
		c.reject(fmt.Sprintf("release title doesn't appear to be %s", wantedAlbum))
	}

	// Format: best recognized format wins; none recognized is fatal.
	best := -1
	for _, f := range c.Parsed.Formats {
		if s, ok := prefs.FormatScores[f]; ok && s > best {
			best = s
		}
	}
	switch {
	case len(c.Parsed.Formats) == 0 && prefs.AllowUnknownFormat && prefs.MinFormatScore > 0:
		// An upgrade search (MinFormatScore set) can't take a format-less
		// title's word for it being better than what's already owned — real
		// music release titles omit the codec constantly (PreferencesFor
		// forces AllowUnknownFormat on for exactly that reason), but "we
		// don't know" is not evidence of "it's better."
		//
		// Found live: this used to only reject when the fixed
		// unknownFormatScore baseline (30) didn't clear MinFormatScore,
		// which silently stopped protecting anything once MinFormatScore
		// itself dropped below 30 — exactly the case for whoever owns the
		// worst-ranked format in a 5-format profile (scored 100/80/60/40/20
		// by list position), where 30 > 20 let a format-less release
		// auto-approve as a "genuine upgrade" purely from that coincidence,
		// never from real evidence. An upgrade search rejects every
		// format-less release outright now, regardless of where the owned
		// format ranks — consistent with the comment above, which already
		// said "not evidence," not "weaker evidence."
		c.reject("not an upgrade over the owned format")
	case len(c.Parsed.Formats) == 0 && prefs.AllowUnknownFormat:
		c.Score += unknownFormatScore
	case len(c.Parsed.Formats) == 0:
		c.reject("no recognized format in release name")
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

	if rel.Size > 0 {
		if rel.Size < prefs.MinSize {
			c.reject("suspiciously small file")
		}
		if rel.Size > prefs.MaxSize {
			c.reject("too large")
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

	c.Approved = len(c.Rejections) == 0
	return c
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
