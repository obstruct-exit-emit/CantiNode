package scanner

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ebookExtensions are the ebook file types the scanner recognizes (per the
// README's format list); comic extensions follow below.
var ebookExtensions = map[string]bool{
	".epub": true,
	".mobi": true,
	".azw3": true,
	".pdf":  true,
}

// IsEbookPath reports whether a filename has one of the ebook extensions
// LibriNode handles (used by the scanner and the download importer).
func IsEbookPath(name string) bool {
	return ebookExtensions[strings.ToLower(filepath.Ext(name))]
}

// comicExtensions are the archive types comic roots scan for.
var comicExtensions = map[string]bool{
	".cbz":  true,
	".cbr":  true,
	".pdf":  true,
	".epub": true,
}

// IsComicPath reports whether a filename is a comic archive.
func IsComicPath(name string) bool {
	return comicExtensions[strings.ToLower(filepath.Ext(name))]
}

var volumeMarker = regexp.MustCompile(`(?i)(?:\bv|\bvol\.?\s*|\bvolume\s+|#)(\d{1,4}(?:\.\d+)?)`)

// unwantedExtensions are file types a book/media download must never contain:
// executables and installers mark a release as spam or malware masquerading as
// the book it claims to be (usenet feeds are rife with these).
var unwantedExtensions = map[string]bool{
	".exe": true, ".scr": true, ".bat": true, ".cmd": true, ".com": true,
	".msi": true, ".vbs": true, ".ps1": true, ".lnk": true, ".apk": true,
	".jar": true, ".iso": true, ".dll": true, ".dmg": true, ".pkg": true,
	".deb": true, ".rpm": true, ".app": true,
}

// IsUnwantedFile reports whether a filename has an executable/installer
// extension — a strong spam/malware signal in a book download. Used by the
// importer to reject (and blocklist) a completed download whose real content
// isn't the book it was named after.
func IsUnwantedFile(name string) bool {
	return unwantedExtensions[strings.ToLower(filepath.Ext(name))]
}

// namesExecutable matches an executable/installer extension appearing as a
// token inside a release name (e.g. "Some.Book.exe" or "Title-scr").
var namesExecutable = regexp.MustCompile(`(?i)[.\-\s](exe|scr|bat|cmd|com|msi|vbs|ps1|lnk|apk|jar|iso|dll|dmg|pkg|deb|rpm|app)\b`)

// NamesExecutable reports whether a release title itself names an executable/
// installer extension — a pre-download spam signal (the real .exe is only seen
// after download, but some junk names it outright).
func NamesExecutable(title string) bool {
	return namesExecutable.MatchString(title)
}

// VolumeFromName extracts a volume/issue number from a filename ("Berserk
// v05", "Berserk Vol. 5", "The Walking Dead #12"); 0 means none found.
func VolumeFromName(name string) float64 {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	if m := volumeMarker.FindStringSubmatch(base); m != nil {
		v, _ := strconv.ParseFloat(m[1], 64)
		return v
	}
	return 0
}

// ParsedFile is the scanner's best guess at what a file is, derived purely
// from its path. Zero fields mean "unknown". AltTitle holds the segment
// after the last " - " when the primary title contains one — how
// "Discworld 8 - Guards! Guards!.epub" (our own naming template's output)
// still matches the book "Guards! Guards!".
type ParsedFile struct {
	Author   string
	Title    string
	AltTitle string
	// ISBN (normalized to ISBN-13) and ASIN parsed from the filename, when it
	// carries one. Embedded-metadata identifiers (epub OPF) are filled in by
	// the scanner, which has the file bytes. Either lets the matcher resolve
	// the file by identifier before falling back to author/title.
	ISBN string
	ASIN string
}

// leadingIndex matches "01 - " / "1.5 - " series-position prefixes.
var leadingIndex = regexp.MustCompile(`^\d+(\.\d+)?\s*-\s*`)

// ParsePath guesses author and title from a path relative to the root
// folder. Recognized layouts:
//
//	Author/Title.epub
//	Author/Series/01 - Title.epub  (series dir ignored, index stripped)
//	Author - Title.epub            (flat)
//	Title.epub                     (title only)
func ParsePath(relPath string) ParsedFile {
	relPath = filepath.ToSlash(relPath)
	parts := strings.Split(relPath, "/")

	base := parts[len(parts)-1]
	base = strings.TrimSuffix(base, filepath.Ext(base))
	stem := base // pre-index-strip, so a leading numeric ISBN isn't eaten
	base = leadingIndex.ReplaceAllString(base, "")

	var p ParsedFile
	p.ISBN = ISBNFromName(stem)
	p.ASIN = ASINFromName(stem)
	if len(parts) >= 2 {
		// First directory is the author by convention.
		p.Author = parts[0]
		p.Title = base
		// "Author - Title" inside an author dir: drop the redundant prefix.
		if prefix, rest, ok := strings.Cut(base, " - "); ok && strings.EqualFold(strings.TrimSpace(prefix), p.Author) {
			p.Title = strings.TrimSpace(rest)
		}
		p.AltTitle = lastDashSegment(p.Title)
		return p
	}

	// Flat file: "Author - Title.ext", else just a title.
	if author, title, ok := strings.Cut(base, " - "); ok {
		p.Author = strings.TrimSpace(author)
		p.Title = strings.TrimSpace(title)
		p.AltTitle = lastDashSegment(p.Title)
		return p
	}
	p.Title = base
	return p
}

// lastDashSegment returns the text after the last " - ", when different from
// the whole ("Discworld 8 - Guards! Guards!" → "Guards! Guards!").
func lastDashSegment(s string) string {
	if i := strings.LastIndex(s, " - "); i >= 0 {
		if seg := strings.TrimSpace(s[i+3:]); seg != "" && seg != s {
			return seg
		}
	}
	return ""
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Normalize reduces a name/title to a matching key: lowercase, punctuation
// collapsed to single spaces, leading English article dropped.
func Normalize(s string) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, article) {
			s = s[len(article):]
			break
		}
	}
	return s
}

// trailingParens matches parenthesized tail chunks — "Title (2011)",
// "Title (retail) (epub)" — which name editions, not the title itself.
var trailingParens = regexp.MustCompile(`(\s*\([^)]*\))+\s*$`)

// TitleKeys returns the normalized match keys for a title: the full title;
// the main title alone when a subtitle is present ("Title: Subtitle"); and
// the title without trailing parentheticals — our own naming templates emit
// "Title (Year)", and release names add "(retail)"-style tags, neither of
// which should defeat a match.
func TitleKeys(title string) []string {
	keys := []string{Normalize(title)}
	add := func(t string) {
		k := Normalize(t)
		if k == "" {
			return
		}
		for _, have := range keys {
			if have == k {
				return
			}
		}
		keys = append(keys, k)
	}
	if main, _, ok := strings.Cut(title, ":"); ok {
		add(main)
	}
	if stripped := trailingParens.ReplaceAllString(title, ""); stripped != title {
		add(stripped)
		if main, _, ok := strings.Cut(stripped, ":"); ok {
			add(main)
		}
	}
	return keys
}
