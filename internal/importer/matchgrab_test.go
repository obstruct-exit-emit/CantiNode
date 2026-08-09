package importer

import (
	"testing"

	"github.com/librinode/librinode/internal/download"
)

// TestMatchGrabTitleFallbackAppliesEvenWithAClientItemID: a torrent grab's
// client item id can be wrong — a pre-existing same-titled item at grab time
// (see qbittorrent.findHash), or a record predating the client-item-id fix
// entirely — so the title fallback must still resolve it instead of leaving
// it unmatched forever just because ClientItemID happens to be non-empty.
func TestMatchGrabTitleFallbackAppliesEvenWithAClientItemID(t *testing.T) {
	pending := []download.GrabRecord{
		{ID: 1, Title: "Dune Messiah", ClientItemID: "wrong-hash"},
	}
	item := &download.Item{ID: "actual-hash", Title: "Dune Messiah"}

	got := matchGrab(pending, item)
	if got == nil || got.ID != 1 {
		t.Fatalf("matchGrab = %v, want grab 1 resolved by title despite its wrong client item id", got)
	}
}

// TestMatchGrabPrefersExactIDMatch: when a grab's client item id is correct,
// it must win over any title coincidence with another pending grab.
func TestMatchGrabPrefersExactIDMatch(t *testing.T) {
	pending := []download.GrabRecord{
		{ID: 1, Title: "Dune Messiah", ClientItemID: "hash-a"},
		{ID: 2, Title: "Dune Messiah", ClientItemID: "hash-b"},
	}
	item := &download.Item{ID: "hash-b", Title: "Dune Messiah"}

	got := matchGrab(pending, item)
	if got == nil || got.ID != 2 {
		t.Fatalf("matchGrab = %v, want grab 2 (exact client item id match)", got)
	}
}
