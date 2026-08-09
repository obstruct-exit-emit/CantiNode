package scanner

// Similarity is the Sørensen–Dice coefficient over character bigrams of two
// already-normalized strings: 0 (nothing in common) to 1 (identical). It
// tolerates the ways a filename drifts from a title — a typo, a dropped or
// swapped word, extra edition noise — where exact and substring matching give
// up. Used only to *suggest* a match a human then confirms, never to attach a
// file on its own.
func Similarity(a, b string) float64 {
	if a == b {
		if a == "" {
			return 0
		}
		return 1
	}
	ba, bb := bigrams(a), bigrams(b)
	if len(ba) == 0 || len(bb) == 0 {
		return 0
	}
	counts := make(map[string]int, len(ba))
	for _, g := range ba {
		counts[g]++
	}
	inter := 0
	for _, g := range bb {
		if counts[g] > 0 {
			counts[g]--
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(ba)+len(bb))
}

// bigrams returns the overlapping 2-rune windows of s (a single-rune string
// yields itself, so short titles still compare).
func bigrams(s string) []string {
	r := []rune(s)
	switch {
	case len(r) == 0:
		return nil
	case len(r) == 1:
		return []string{string(r)}
	}
	out := make([]string, 0, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out = append(out, string(r[i:i+2]))
	}
	return out
}
