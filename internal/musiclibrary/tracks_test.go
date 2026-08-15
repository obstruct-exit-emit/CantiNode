package musiclibrary

import "testing"

func TestGetOrCreateTrackCreatesThenReuses(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}

	t1, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack: %v", err)
	}
	if t1.AlbumID != album.ID {
		t.Errorf("AlbumID = %d, want %d", t1.AlbumID, album.ID)
	}

	t2, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (second call): %v", err)
	}
	if t2.ID != t1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", t2.ID, t1.ID)
	}

	list, err := db.ListTracksByAlbum(album.ID)
	if err != nil {
		t.Fatalf("ListTracksByAlbum: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
}

func TestGetTrackNotFound(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.GetTrack(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListTracksByAlbumEmptyIsNotNil guards against a real bug: a nil
// slice marshals to JSON null, not [], which would crash a web UI doing
// .length/.map() on an empty result.
func TestListTracksByAlbumEmptyIsNotNil(t *testing.T) {
	db := newTestStore(t)
	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListTracksByAlbum(album.ID)
	if err != nil {
		t.Fatalf("ListTracksByAlbum: %v", err)
	}
	if list == nil {
		t.Error("ListTracksByAlbum returned nil for an empty result, want a non-nil empty slice")
	}
}
