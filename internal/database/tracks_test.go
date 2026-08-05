package database

import "testing"

func TestGetOrCreateTrackCreatesThenReuses(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	artist, err := db.GetOrCreateArtist(ctx, "artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(ctx, artist.ID, "album-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}

	t1, err := db.GetOrCreateTrack(ctx, album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000)
	if err != nil {
		t.Fatalf("GetOrCreateTrack: %v", err)
	}
	if t1.AlbumID != album.ID {
		t.Errorf("AlbumID = %d, want %d", t1.AlbumID, album.ID)
	}

	t2, err := db.GetOrCreateTrack(ctx, album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000)
	if err != nil {
		t.Fatalf("GetOrCreateTrack (second call): %v", err)
	}
	if t2.ID != t1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", t2.ID, t1.ID)
	}

	list, err := db.ListTracksByAlbum(ctx, album.ID)
	if err != nil {
		t.Fatalf("ListTracksByAlbum: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
}

func TestGetTrackNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetTrack(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListTracksByAlbumEmptyIsNotNil guards against a real bug — see
// TestListRootFoldersEmptyIsNotNil's doc comment for the full story.
func TestListTracksByAlbumEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	artist, err := db.GetOrCreateArtist(t.Context(), "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(t.Context(), artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListTracksByAlbum(t.Context(), album.ID)
	if err != nil {
		t.Fatalf("ListTracksByAlbum: %v", err)
	}
	if list == nil {
		t.Error("ListTracksByAlbum returned nil for an empty result, want a non-nil empty slice")
	}
}
