package database

import "testing"

func TestGetOrCreateArtistCreatesThenReuses(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	a1, err := db.GetOrCreateArtist(ctx, "mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if a1.ID == 0 {
		t.Error("expected nonzero ID")
	}

	a2, err := db.GetOrCreateArtist(ctx, "mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist (second call): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}

	got, err := db.GetArtist(ctx, a1.ID)
	if err != nil {
		t.Fatalf("GetArtist: %v", err)
	}
	if got.Name != "Boards of Canada" {
		t.Errorf("Name = %q, want Boards of Canada", got.Name)
	}
}

func TestGetArtistNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetArtist(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListArtistsEmptyIsNotNil guards against a real bug — see
// TestListRootFoldersEmptyIsNotNil's doc comment for the full story.
func TestListArtistsEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	list, err := db.ListArtists(t.Context())
	if err != nil {
		t.Fatalf("ListArtists: %v", err)
	}
	if list == nil {
		t.Error("ListArtists returned nil for an empty result, want a non-nil empty slice")
	}
}
