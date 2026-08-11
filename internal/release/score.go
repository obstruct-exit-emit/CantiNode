package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
	"github.com/cantinode/cantinode/internal/relname"
)

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

// Score evaluates one release against generic checks: format, size, health.
func Score(rel indexer.Release, prefs Preferences) Candidate {
	c := Candidate{Release: rel, Parsed: Parse(rel.Title)}

	// Spam guard: a release whose name states an executable/installer extension
	// is malware masquerading as the real content — reject before it can be
	// grabbed. (Most spam hides a clean name and is only caught at import;
	// this catches the ones that name it outright.)
	if relname.NamesExecutable(rel.Title) {
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
	case len(c.Parsed.Formats) == 0 && prefs.AllowUnknownFormat && prefs.MinFormatScore > 0 && unknownFormatScore <= prefs.MinFormatScore:
		// An upgrade search (MinFormatScore set) can't take a format-less
		// title's word for it being better than what's already owned — real
		// music release titles omit the codec constantly (PreferencesFor
		// forces AllowUnknownFormat on for exactly that reason), but "we
		// don't know" is not evidence of "it's better." Scored as if it
		// stated the unknown-format baseline score, same rejection rule as
		// a release that did name its format.
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
