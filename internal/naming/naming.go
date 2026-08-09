// Package naming renders *arr-style file naming templates. Tokens are
// {Author Name}-style placeholders; unknown values render empty and a
// cleanup pass removes the separators they leave dangling, so one template
// works for books both in and out of a series.
package naming

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TokenData is everything a template can reference. Zero values mean
// "unknown" and render as empty.
type TokenData struct {
	AuthorName     string
	AuthorSortName string
	BookTitle      string
	SeriesTitle    string
	SeriesPosition float64
	ReleaseYear    string
}

// Tokens lists the supported placeholders (shown in the settings UI).
// {Series Position 00} is the zero-padded variant, so "Vol. 01" sorts before
// "Vol. 10" in plain file listings.
var Tokens = []string{
	"{Author Name}",
	"{Author SortName}",
	"{Book Title}",
	"{Series Title}",
	"{Series Position}",
	"{Series Position 00}",
	"{Release Year}",
}

func (d TokenData) value(token string) (string, bool) {
	switch token {
	case "Author Name":
		return d.AuthorName, true
	case "Author SortName":
		return d.AuthorSortName, true
	case "Book Title":
		return d.BookTitle, true
	case "Series Title":
		return d.SeriesTitle, true
	case "Series Position":
		if d.SeriesPosition == 0 {
			return "", true
		}
		return strconv.FormatFloat(d.SeriesPosition, 'f', -1, 64), true
	case "Series Position 00":
		if d.SeriesPosition == 0 {
			return "", true
		}
		s := strconv.FormatFloat(d.SeriesPosition, 'f', -1, 64)
		// Pad the integer part to two digits (5 → 05, 5.5 → 05.5, 100 → 100).
		if i := strings.IndexByte(s, '.'); i == 1 {
			s = "0" + s
		} else if i < 0 && len(s) == 1 {
			s = "0" + s
		}
		return s, true
	case "Release Year":
		return d.ReleaseYear, true
	}
	return "", false
}

var tokenPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// Format renders one path segment from a template. The result is sanitized
// for cross-platform filesystem use; path separators in the template are NOT
// allowed (compose segments with filepath.Join instead).
func Format(template string, data TokenData) string {
	out := tokenPattern.ReplaceAllStringFunc(template, func(m string) string {
		name := strings.TrimSpace(m[1 : len(m)-1])
		if v, ok := data.value(name); ok {
			return v
		}
		return m // unknown token stays literal so typos are visible
	})
	return sanitize(cleanup(out))
}

var (
	emptyParens   = regexp.MustCompile(`\(\s*\)|\[\s*\]|\{\s*\}`)
	multiSpace    = regexp.MustCompile(`\s{2,}`)
	danglingSep   = regexp.MustCompile(`(^\s*[-–—#:,.]\s*)+|(\s*[-–—#:,]\s*)+$`)
	repeatedSep   = regexp.MustCompile(`(\s*-\s*){2,}`)
	orphanMarkers = regexp.MustCompile(`\s[#]\s|^[#]\s|\s[#]$`)
)

// cleanup removes artifacts left by empty tokens: "() ", leading/trailing
// " - ", doubled separators, stray "#" markers, extra spaces.
func cleanup(s string) string {
	s = emptyParens.ReplaceAllString(s, "")
	s = orphanMarkers.ReplaceAllString(s, " ")
	s = repeatedSep.ReplaceAllString(s, " - ")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = danglingSep.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// FormatPath renders a folder template that may span several path levels
// ("{Author Name}/{Book Title} ({Release Year})"). Each "/"-separated part is
// rendered and sanitized as its own segment; parts that come out empty drop
// away entirely, so a year-less book nests one level shallower instead of
// leaving an "_" directory. The result uses the OS path separator.
func FormatPath(template string, data TokenData) string {
	var segments []string
	for _, part := range strings.Split(template, "/") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		seg := sanitize(cleanup(tokenPattern.ReplaceAllStringFunc(part, func(m string) string {
			name := strings.TrimSpace(m[1 : len(m)-1])
			if v, ok := data.value(name); ok {
				return v
			}
			return m
		})))
		if seg == "_" {
			continue // the part rendered empty — drop the level
		}
		segments = append(segments, seg)
	}
	if len(segments) == 0 {
		return "_"
	}
	return filepath.Join(segments...)
}

var forbidden = regexp.MustCompile(`[<>:"/\\|?*]`)

// sanitize makes a rendered segment safe as a single file/directory name on
// Windows and Linux.
func sanitize(s string) string {
	s = forbidden.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .") // Windows rejects trailing dots/spaces
	if s == "" {
		s = "_"
	}
	return s
}
