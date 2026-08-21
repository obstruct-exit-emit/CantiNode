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

	t1, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack: %v", err)
	}
	if t1.AlbumID != album.ID {
		t.Errorf("AlbumID = %d, want %d", t1.AlbumID, album.ID)
	}

	t2, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "", "")
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

// TestGetOrCreateTrackScopedPerAlbum is the regression test for a real
// live bug: the same MusicBrainz recording legitimately appearing on two
// different releases (a single also included on its parent album) used
// to collapse onto one globally-shared track row (tracks.mbid was
// column-level UNIQUE) — a Blind Melon "Change" single track, correctly
// tagged with the single's own release, still resolved to the self-titled
// album's own pre-existing "Change" track, and Organize then refused to
// place the single's own file at that track's already-occupied
// destination. The same recording mbid under two different albums must
// now get two separate track rows.
func TestGetOrCreateTrackScopedPerAlbum(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("artist-mbid", "Blind Melon", "Blind Melon")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-album", "Blind Melon", "1992", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	single, err := db.GetOrCreateAlbum(artist.ID, "single-mbid", "rg-single", "Change", "1994", "Single")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (single): %v", err)
	}

	albumTrack, err := db.GetOrCreateTrack(album.ID, "rec-change", "Change", 6, 1, 220000, "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (album): %v", err)
	}
	singleTrack, err := db.GetOrCreateTrack(single.ID, "rec-change", "Change", 1, 1, 220000, "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (single): %v", err)
	}

	if albumTrack.ID == singleTrack.ID {
		t.Fatalf("both albums got the same track row (ID %d) for the same recording — expected two separate rows, one per album", albumTrack.ID)
	}
	if singleTrack.AlbumID != single.ID {
		t.Errorf("singleTrack.AlbumID = %d, want %d", singleTrack.AlbumID, single.ID)
	}

	// Re-fetching under the SAME album must still dedupe, same as before —
	// only cross-album sharing changed.
	again, err := db.GetOrCreateTrack(album.ID, "rec-change", "Change", 6, 1, 220000, "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (album, second call): %v", err)
	}
	if again.ID != albumTrack.ID {
		t.Errorf("second call under the same album created a new row: ID = %d, want %d", again.ID, albumTrack.ID)
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
