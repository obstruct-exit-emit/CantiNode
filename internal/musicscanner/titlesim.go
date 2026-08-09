package musicscanner

import "strings"

// titleSimilarity scores how alike two track titles are, 0 (nothing in
// common) to 1 (identical after normalization) — a case/punctuation-
// insensitive Levenshtein ratio, used by folder_match.go's slotTrack to
// match a local file's own title against a candidate release's own
// (already-fetched) tracklist when track/disc numbers alone aren't
// enough to place it.
func titleSimilarity(a, b string) float64 {
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

// normalizeTitle lowercases and strips everything but letters/digits/
// spaces, collapsing runs of whitespace — punctuation/case differences
// between a file's own tag and MusicBrainz's title shouldn't count
// against a real match (e.g. "Layla (Acoustic)" vs "Layla - Acoustic").
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
