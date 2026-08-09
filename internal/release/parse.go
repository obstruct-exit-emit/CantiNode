// Package release parses raw indexer release titles into structured data and
// scores them against wanted books. This is the decision engine behind
// interactive search now and automatic grabbing later.
package release

import (
	"regexp"
	"strconv"
	"strings"
)

// Parsed is what a release title tells us, best-effort. Zero values mean
// "not stated".
type Parsed struct {
	Author   string   `json:"author,omitempty"`
	Title    string   `json:"title,omitempty"`
	Year     int      `json:"year,omitempty"`
	Formats  []string `json:"formats,omitempty"` // ebook and audio format tokens
	Language string   `json:"language,omitempty"`
	Retail   bool     `json:"retail"`
	Group    string   `json:"group,omitempty"`
	// Audio-specific fields.
	Narrator string `json:"narrator,omitempty"`
	Bitrate  int    `json:"bitrate,omitempty"` // kbps
	Abridged bool   `json:"abridged"`
	// Manga/comic volume or issue number ("v05", "Vol. 5", "#12"); 0 when
	// not stated.
	Volume float64 `json:"volume,omitempty"`
	// VolumeEnd is the range end when the release spans volumes
	// ("v01-v12", "Vol. 1-12"); 0 when the release names a single volume.
	VolumeEnd float64 `json:"volumeEnd,omitempty"`
	// Pack marks releases that declare themselves complete runs
	// ("Complete", "Collection") — series packs even without a range.
	Pack bool `json:"pack,omitempty"`
}

var mediaFormats = map[string]bool{
	// ebook
	"epub": true, "mobi": true, "azw3": true, "pdf": true,
	// audiobook
	"m4b": true, "m4a": true, "mp3": true, "flac": true, "opus": true,
	// manga/comic
	"cbz": true, "cbr": true,
}

// languages maps release-title tokens to a normalized language name.
var languages = map[string]string{
	"english": "english", "eng": "english", "en": "english",
	"german": "german", "ger": "german", "deutsch": "german", "de": "german",
	"french": "french", "fre": "french", "fr": "french",
	"spanish": "spanish", "spa": "spanish", "es": "spanish",
	"italian": "italian", "ita": "italian", "it": "italian",
	"dutch": "dutch", "nl": "dutch",
	"russian": "russian", "rus": "russian",
	"portuguese": "portuguese", "por": "portuguese",
	"polish": "polish", "pol": "polish",
	"japanese": "japanese", "jpn": "japanese",
}

var (
	bracketTags     = regexp.MustCompile(`[\[({]([^\][(){}]*)[\])}]`)
	yearToken       = regexp.MustCompile(`^(19|20)\d{2}$`)
	sceneGroup      = regexp.MustCompile(`-([A-Za-z0-9][A-Za-z0-9_]{1,15})$`)
	byPattern       = regexp.MustCompile(`(?i)^(.+?)\s+by\s+(.+)$`)
	narratorPattern = regexp.MustCompile(`(?i)\b(read|narrated)\s+by\s+([A-Za-z][A-Za-z .'’-]*)`)
	bitrateToken    = regexp.MustCompile(`^(\d{2,3})\s?(k|kbps)$`)
	volumeWords     = regexp.MustCompile(`(?i)\b(?:vol|volume)\.?\s*(\d+(?:\.\d+)?)`)
	volumeToken     = regexp.MustCompile(`^(?:v(\d{1,3}(?:\.\d+)?)|#(\d{1,4}(?:\.\d+)?))$`)
	// Volume ranges: "v01-v12", "Vol. 1-12", "volumes 1~12", "#1-50",
	// "c001-c180". A prefix (v/vol/#/c) is required on at least the first
	// number so year spans ("2020-2021") never read as volumes. (# carries
	// no leading \b — a word boundary never precedes a symbol.)
	volumeRange = regexp.MustCompile(`(?i)(?:\b(?:vol(?:ume)?s?\.?\s*|[vc])|#)(\d{1,4}(?:\.\d+)?)\s*[-–~]\s*(?:[vc#]\s*)?(\d{1,4}(?:\.\d+)?)\b`)
	packWords   = regexp.MustCompile(`(?i)\b(complete|collection|completa|full\s+set)\b`)
)

// Parse extracts structured info from one release title.
func Parse(title string) Parsed {
	var p Parsed
	working := strings.TrimSpace(title)

	// Scene-style dotted names: many dots, few spaces.
	if strings.Count(working, " ") <= 1 && strings.Count(working, ".") >= 3 {
		if m := sceneGroup.FindStringSubmatch(working); m != nil {
			p.Group = m[1]
			working = strings.TrimSuffix(working, m[0])
		}
		working = strings.ReplaceAll(working, ".", " ")
	}

	// Narrator credit ("read by X" / "narrated by X") — extract before
	// tokenizing so the name doesn't pollute the title.
	if m := narratorPattern.FindStringSubmatch(working); m != nil {
		p.Narrator = strings.TrimSpace(m[2])
		working = strings.Replace(working, m[0], " ", 1)
	}

	// Volume ranges first ("v01-v12", "Vol. 1-12") — a pack's span, with the
	// start doubling as the single-volume field. Then worded single volumes
	// ("Vol. 5"); single-token forms (v05, #12) are handled during token
	// scanning.
	if m := volumeRange.FindStringSubmatch(working); m != nil {
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		if end > start {
			p.Volume, p.VolumeEnd = start, end
			working = strings.Replace(working, m[0], " ", 1)
		}
	}
	if m := volumeWords.FindStringSubmatch(working); m != nil {
		if p.Volume == 0 {
			p.Volume, _ = strconv.ParseFloat(m[1], 64)
		}
		working = strings.Replace(working, m[0], " ", 1)
	}
	if packWords.MatchString(working) {
		p.Pack = true
	}

	// Pull bracketed tags out: they hold formats, language, year, retail.
	working = bracketTags.ReplaceAllStringFunc(working, func(m string) string {
		inner := m[1 : len(m)-1]
		for _, tok := range strings.FieldsFunc(inner, func(r rune) bool {
			return r == ' ' || r == ',' || r == '/' || r == '|' || r == '+'
		}) {
			p.absorbToken(tok)
		}
		return " "
	})

	// Scan remaining words for trailing metadata tokens.
	words := strings.Fields(working)
	kept := make([]string, 0, len(words))
	for _, w := range words {
		if p.absorbToken(strings.Trim(w, ",;")) {
			continue
		}
		kept = append(kept, w)
	}
	working = strings.Join(kept, " ")
	working = strings.TrimSpace(strings.Trim(working, "-–"))

	// Author/title split: "Author - [Series NN -] Title" or "Title by Author".
	if parts := splitDash(working); len(parts) >= 2 {
		p.Author = strings.TrimSpace(parts[0])
		p.Title = strings.TrimSpace(parts[len(parts)-1])
	} else if m := byPattern.FindStringSubmatch(working); m != nil {
		p.Title = strings.TrimSpace(m[1])
		p.Author = strings.TrimSpace(m[2])
	} else {
		p.Title = working
	}
	return p
}

// absorbToken records a metadata token, reporting whether it was one.
func (p *Parsed) absorbToken(tok string) bool {
	t := strings.ToLower(strings.TrimSpace(tok))
	switch {
	case t == "":
		return true
	case mediaFormats[t]:
		for _, f := range p.Formats {
			if f == t {
				return true
			}
		}
		p.Formats = append(p.Formats, t)
		return true
	case t == "retail":
		p.Retail = true
		return true
	case t == "abridged":
		p.Abridged = true
		return true
	case t == "unabridged":
		return true // stated but it's the default assumption
	case yearToken.MatchString(t):
		if p.Year == 0 {
			p.Year, _ = strconv.Atoi(t)
		}
		return true
	}
	if m := bitrateToken.FindStringSubmatch(t); m != nil {
		if p.Bitrate == 0 {
			p.Bitrate, _ = strconv.Atoi(m[1])
		}
		return true
	}
	if m := volumeToken.FindStringSubmatch(t); m != nil {
		if p.Volume == 0 {
			num := m[1]
			if num == "" {
				num = m[2]
			}
			p.Volume, _ = strconv.ParseFloat(num, 64)
		}
		return true
	}
	if lang, ok := languages[t]; ok {
		if p.Language == "" {
			p.Language = lang
		}
		return true
	}
	return false
}

func splitDash(s string) []string {
	parts := strings.Split(s, " - ")
	out := parts[:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}
