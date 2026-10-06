package relname

import "strings"

// TitleSimilarity scores how alike two track titles are, 0 (nothing in
// common) to 1 (identical after normalization) — a case/punctuation-
// insensitive Levenshtein ratio. Used by internal/musicscanner's slotTrack
// to match a local file's own title against a candidate release's own
// (already-fetched) tracklist when track/disc numbers alone aren't enough
// to place it, and by internal/importer's swapUpgradedFiles to recognize
// "the same song, a different MusicBrainz recording ID" across an upgrade
// (a remaster MusicBrainz treats as a distinct recording from the
// original, even though it's clearly the same track at the same position).
func TitleSimilarity(a, b string) float64 {
	na, nb := normalizeTitle(a), normalizeTitle(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	dist := levenshtein(na, nb)
	maxLen := len(na)
	if len(nb) > maxLen {
		maxLen = len(nb)
	}
	return 1 - float64(dist)/float64(maxLen)
}

// TitleIsPrefixOf reports whether one of a/b, once normalized, is a leading
// prefix of the other — true for "Spectres" vs "Spectres (Instrumental
// Version)" or "Layla" vs "Layla (Acoustic)": a file's title tag missing
// the edition/version qualifier the release's own title carries (or vice
// versa), the single most common reason a real, correct title legitimately
// fails to clear TitleSimilarity's own threshold. Deliberately a separate
// signal rather than folded into TitleSimilarity itself: a long qualifier
// appended to a short base title scores low on raw edit-distance ratio by
// pure length coincidence, indistinguishable there from two genuinely
// unrelated titles that happen to land in a similar length/distance range —
// confirmed live, where "Spectres" vs "Spectres (instrumental version)"
// (0.276) and a real mismatch, "The Christmas Song" vs "The Weight"
// (0.278), scored within 0.002 of each other on TitleSimilarity alone.
func TitleIsPrefixOf(a, b string) bool {
	na, nb := normalizeTitle(a), normalizeTitle(b)
	if na == "" || nb == "" {
		return false
	}
	return strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
}

// TitleContains reports whether needle, once normalized, appears anywhere
// within haystack, also normalized — not just as a leading prefix (see
// TitleIsPrefixOf). Deliberately a separate, narrower-purpose signal from
// that one: calling this on an already artist-stripped title would
// reintroduce exactly the false-positive TitleIsPrefixOf itself was found
// to cause for a band-name-prefixed sequel/compilation album (see
// internal/release's own albumRelevant and its doc comment on why that
// fallback was removed there) — a real album title starting with the
// wanted one still "contains" it. This is for the opposite, narrower
// situation instead: haystack is a release title a normal Author/Title
// split failed to separate at all, so it's the artist name, the real
// album title, AND surrounding release-group/codec noise all run
// together with no word boundary signal left to work with — found live,
// "The Beatles-All You Need Is Love-16BIT-WEB-FLAC-2026-OBZEN" (hyphens
// glued straight to words, so Parse correctly declines to split it — see
// sanitizeReleaseTitle's own "glued dash left alone" rule) scored as not
// matching its own exact, correct album title on TitleSimilarity alone,
// since the surrounding noise dilutes the ratio too far. Containment
// still correctly tells this apart from a genuinely different release
// sharing every word in a different order — "Love Is All You Need"
// contains none of "All You Need Is Love" as a contiguous run.
func TitleContains(haystack, needle string) bool {
	nh, nn := normalizeTitle(haystack), normalizeTitle(needle)
	if nh == "" || nn == "" {
		return false
	}
	return strings.Contains(nh, nn)
}

// normalizeTitle lowercases and strips everything but letters/digits/
// spaces, collapsing runs of whitespace — punctuation/case differences
// between a file's own tag and MusicBrainz's title shouldn't count against
// a real match (e.g. "Layla (Acoustic)" vs "Layla - Acoustic").
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// levenshtein is a plain O(len(a)*len(b)) edit-distance implementation —
// fine here since a track title is a handful of words at most (bounded,
// small), matching this codebase's existing preference for hand-rolling
// something this size over adding a dependency (see internal/tagwriter's
// hand-rolled ID3v2 writer).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
