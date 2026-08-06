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

// TestGetOrCreateAlbumDedupesByReleaseGroupNotRelease guards against the
// original bug this dedup key change fixes: MusicBrainz recordings from
// the very same physical album can independently resolve to different
// release editions (see musicbrainz.Recording.BestRelease), so keying
// album identity on mbid alone used to create one albums row per edition
// instead of one per canonical album.
func TestGetOrCreateAlbumDedupesByReleaseGroupNotRelease(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	artist, err := db.GetOrCreateArtist(ctx, "artist-mbid", "Derek and the Dominos", "Derek and the Dominos")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}

	a1, err := db.GetOrCreateAlbum(ctx, artist.ID, "release-edition-2011", "rg-layla", "Layla and Other Assorted Love Songs", "2011", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (2011 edition): %v", err)
	}

	a2, err := db.GetOrCreateAlbum(ctx, artist.ID, "release-edition-1989", "rg-layla", "Layla and Other Assorted Love Songs", "1989", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (1989 edition): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second edition of the same release group created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}
	if a2.Title != "Layla and Other Assorted Love Songs" || a2.MBID != "release-edition-2011" {
		t.Errorf("second call should return the first-recorded row as-is, got %+v", a2)
	}
}

func TestGetAlbumNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetAlbum(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListAlbumsByArtistEmptyIsNotNil guards against a real bug — see
// TestListRootFoldersEmptyIsNotNil's doc comment for the full story.
func TestListAlbumsByArtistEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	artist, err := db.GetOrCreateArtist(t.Context(), "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListAlbumsByArtist(t.Context(), artist.ID)
	if err != nil {
		t.Fatalf("ListAlbumsByArtist: %v", err)
	}
	if list == nil {
		t.Error("ListAlbumsByArtist returned nil for an empty result, want a non-nil empty slice")
	}
}
