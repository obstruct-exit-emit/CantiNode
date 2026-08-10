// Package relname holds small, generic release-name text utilities used by
// release scoring (internal/release) and download-queue enrichment
// (internal/api) — normalizing a title into a matching key, and flagging a
// release name that states an executable/installer extension outright (a
// pre-download spam signal).
package relname

import (
	"regexp"
	"strings"
)

// namesExecutable matches an executable/installer extension appearing as a
// token inside a release name (e.g. "Some.Release.exe" or "Title-scr").
var namesExecutable = regexp.MustCompile(`(?i)[.\-\s](exe|scr|bat|cmd|com|msi|vbs|ps1|lnk|apk|jar|iso|dll|dmg|pkg|deb|rpm|app)\b`)

// NamesExecutable reports whether a release title itself names an executable/
// installer extension — a pre-download spam signal (the real .exe is only
// seen after download, but some junk names it outright).
func NamesExecutable(title string) bool {
	return namesExecutable.MatchString(title)
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
