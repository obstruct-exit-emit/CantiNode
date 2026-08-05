package database

import "testing"

func TestGetOrCreateAlbumCreatesThenReuses(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	artist, err := db.GetOrCreateArtist(ctx, "artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}

	a1, err := db.GetOrCreateAlbum(ctx, artist.ID, "album-mbid", "rg-mbid", "Music Has the Right to Children", "1998-04-20", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	if a1.ArtistID != artist.ID {
		t.Errorf("ArtistID = %d, want %d", a1.ArtistID, artist.ID)
	}

	a2, err := db.GetOrCreateAlbum(ctx, artist.ID, "album-mbid", "rg-mbid", "Music Has the Right to Children", "1998-04-20", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (second call): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}

	got, err := db.GetAlbum(ctx, a1.ID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if got.Title != "Music Has the Right to Children" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestGetAlbumNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetAlbum(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
