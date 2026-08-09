package scanner

import (
	"regexp"
	"strings"
)

// Stable identifiers pulled from a filename (or embedded metadata): an ISBN or
// an Amazon ASIN. An identifier match is definitive — it beats any title guess
// — so extraction is deliberately strict: an ISBN must pass its checksum (any
// 13-digit run can't masquerade as one), and everything normalizes to a single
// canonical form (ISBN-13) so the file and the edition compare equal regardless
// of which form each recorded.

// isbnRun matches a candidate ISBN inside a larger string: 10–13 digits with
// optional internal hyphens, ending in a digit or the ISBN-10 check char X. The
// checksum test below rejects the false positives this loose shape lets in
// (years, ids, phone numbers).
var isbnRun = regexp.MustCompile(`[0-9][0-9-]{8,16}[0-9xX]`)

// asinRun matches an Amazon ASIN token: "B0" followed by 8 uppercase
// alphanumerics (the modern book/Kindle form, e.g. B0072XL8BC). The "B0" anchor
// (not just "B") keeps a 10-letter all-caps word from masquerading as one, and
// the pattern is case-sensitive on purpose — lowercase look-alikes aren't ASINs.
var asinRun = regexp.MustCompile(`\bB0[0-9A-Z]{8}\b`)

// ISBNFromName returns the first valid ISBN in s, normalized to ISBN-13, or ""
// when none is present. Hyphenation is tolerated; the checksum must hold.
func ISBNFromName(s string) string {
	for _, run := range isbnRun.FindAllString(s, -1) {
		if isbn := NormalizeISBN(run); isbn != "" {
			return isbn
		}
	}
	return ""
}

// ASINFromName returns the first ASIN token in s, or "".
func ASINFromName(s string) string {
	// Not every B########## is an ASIN, but the token shape is distinctive
	// enough (and only consulted after ISBN) that the first one wins.
	return asinRun.FindString(s)
}

// NormalizeISBN strips separators, validates the checksum, and returns the
// ISBN-13 form (converting a valid ISBN-10). It returns "" for anything that
// isn't a checksum-valid ISBN.
func NormalizeISBN(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'x' || r == 'X':
			b.WriteByte('X')
		}
	}
	s := b.String()
	switch len(s) {
	case 13:
		if strings.ContainsRune(s, 'X') || !validISBN13(s) {
			return ""
		}
		return s
	case 10:
		if !validISBN10(s) {
			return ""
		}
		return isbn10to13(s)
	default:
		return ""
	}
}

// validISBN13: sum of digits with alternating 1/3 weights is a multiple of 10.
func validISBN13(s string) bool {
	sum := 0
	for i := 0; i < 13; i++ {
		d := int(s[i] - '0')
		if i%2 == 0 {
			sum += d
		} else {
			sum += 3 * d
		}
	}
	return sum%10 == 0
}

// validISBN10: sum of digit*(10..1) is a multiple of 11; the final char may be
// 'X' for 10.
func validISBN10(s string) bool {
	sum := 0
	for i := 0; i < 10; i++ {
		var d int
		if s[i] == 'X' {
			if i != 9 {
				return false // X is only ever the check digit
			}
			d = 10
		} else {
			d = int(s[i] - '0')
		}
		sum += d * (10 - i)
	}
	return sum%11 == 0
}

// isbn10to13 converts a valid ISBN-10 to ISBN-13: prefix 978, drop the old
// check digit, recompute the ISBN-13 check digit.
func isbn10to13(s string) string {
	core := "978" + s[:9]
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(core[i] - '0')
		if i%2 == 0 {
			sum += d
		} else {
			sum += 3 * d
		}
	}
	check := (10 - sum%10) % 10
	return core + string(rune('0'+check))
}
