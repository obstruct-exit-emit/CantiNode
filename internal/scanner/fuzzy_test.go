package scanner

import "testing"

func TestSimilarity(t *testing.T) {
	// Identical (non-empty) → 1; empty pair → 0.
	if got := Similarity("dune messiah", "dune messiah"); got != 1 {
		t.Errorf("identical = %v, want 1", got)
	}
	if got := Similarity("", ""); got != 0 {
		t.Errorf("empty pair = %v, want 0", got)
	}

	// A single-character typo stays high; a different title stays low. The
	// matcher's floor is 0.72, so these must land on the right side of it.
	typo := Similarity("the colour of magic", "the color of magic")
	if typo < 0.72 {
		t.Errorf("one-letter typo similarity = %.3f, want >= 0.72", typo)
	}
	unrelated := Similarity("the colour of magic", "coraline")
	if unrelated >= 0.72 {
		t.Errorf("unrelated similarity = %.3f, want < 0.72", unrelated)
	}

	// Symmetric.
	if a, b := Similarity("mort", "mrot"), Similarity("mrot", "mort"); a != b {
		t.Errorf("not symmetric: %.3f vs %.3f", a, b)
	}
}
