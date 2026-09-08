package musicbrainz

import (
	"regexp"
	"strings"
)

// releaseJunkPattern matches audio-format/rip-quality tokens commonly
// tacked onto a release folder or filename by rippers and scene/P2P
// release groups — e.g. "Layla and Other Assorted Love Songs SHM-CD" or
// "... (Polydor.2011) 24-96 hdtracks". These are never part of a real
// MusicBrainz release title, but confusing enough to Lucene's query
// parser (SHM-CD in particular isn't a real word) that including them
// can turn a good fuzzy match into no match at all.
//
// Deliberately narrow: only unambiguous format/source tokens, not words
// like "Remastered"/"Deluxe"/"Anniversary" — those genuinely are part of
// some official release titles, and stripping them risks matching the
// wrong release instead of just failing to match at all.
var releaseJunkPattern = regexp.MustCompile(`(?i)\b(SHM-?CD|HDCD|SACD|MFSL|K2HD|DSD|FLAC|ALAC|APE|WAVE?|MP3|WEB|CD-?DA|HD-?Tracks|\d{2}-\d{2,3}|\d{2}/\d{2,3}|\d{1,2}\s?-?bit|\d{2,3}(\.\d)?\s?-?kHz|\d{2,3}\s?-?kbps)\b`)

// emptyBracketsPattern cleans up brackets/parens left empty (or holding
// only now-collapsed whitespace) once whatever junk they only contained
// (e.g. "(24-96 FLAC)") is stripped.
var emptyBracketsPattern = regexp.MustCompile(`[([]\s*[)\]]`)

// innerBracketSpacePattern trims a leftover space directly inside an
// opening/closing bracket (e.g. "(24-Bit Remaster)" losing "24-Bit"
// becomes "( Remaster)" before this tidies it to "(Remaster)").
var innerBracketSpacePattern = regexp.MustCompile(`([([])\s+|\s+([)\]])`)

// standaloneSeparatorPattern matches a whitespace-padded subtitle
// separator — " - ", " : ", " – ", " — " — collapsed to a single space
// rather than left in place. Confirmed live: MusicBrainz's own search
// treats a space-padded punctuation mark as its own token position, which
// a phrase query needs an exact adjacent match for — "The Mystery Of Time
// - A Rock Epic" (a file's own tag, hyphen-separated) returned zero
// results against MusicBrainz's real title "The Mystery of Time: A Rock
// Epic" (colon, no surrounding spaces) even though every real word
// matches; removing the space-padded separator entirely ("The Mystery Of
// Time A Rock Epic") found it immediately. A colon/dash glued directly to
// a word (no space, e.g. "Time:") is left alone — only the padded,
// stray-token form is the problem.
var standaloneSeparatorPattern = regexp.MustCompile(`\s+[-–—:]\s+`)

// sanitizeReleaseTitle strips known rip/format junk from a release title
// before it's sent to MusicBrainz as part of a recording search — see
// SearchRecordings, the one place both internal/scanner's automatic
// matcher and the manual-review "Search MusicBrainz" action ultimately
// go through, so fixing it here covers both automatically.
func sanitizeReleaseTitle(s string) string {
	cleaned := releaseJunkPattern.ReplaceAllString(s, "")
	cleaned = innerBracketSpacePattern.ReplaceAllString(cleaned, "$1$2")
	cleaned = emptyBracketsPattern.ReplaceAllString(cleaned, "")
	cleaned = standaloneSeparatorPattern.ReplaceAllString(cleaned, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.Trim(cleaned, " -–—,")
	return cleaned
}
