package musiclibrary

import (
	"testing"
	"time"
)

func TestGetOrCreateAlbumCreatesThenReuses(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}

	a1, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Music Has the Right to Children", "1998-04-20", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	if a1.ArtistID != artist.ID {
		t.Errorf("ArtistID = %d, want %d", a1.ArtistID, artist.ID)
	}

	a2, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Music Has the Right to Children", "1998-04-20", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (second call): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}

	got, err := db.GetAlbum(a1.ID)
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
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("artist-mbid", "Derek and the Dominos", "Derek and the Dominos")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}

	a1, err := db.GetOrCreateAlbum(artist.ID, "release-edition-2011", "rg-layla", "Layla and Other Assorted Love Songs", "2011", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (2011 edition): %v", err)
	}

	a2, err := db.GetOrCreateAlbum(artist.ID, "release-edition-1989", "rg-layla", "Layla and Other Assorted Love Songs", "1989", "Album")
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

// TestGetOrCreateAlbumRecoversFromMBIDCollisionAcrossReleaseGroups is the
// regression test for a real bug found live: two tracks of the same
// physical release resolved (via musicbrainz.Recording.BestRelease) to
// different release-group MBIDs — a genuine MusicBrainz data quirk, the
// same class of issue the Birdy/duplicate-recording case earlier already
// showed this catalog has — so the second call's getAlbumByReleaseGroupMBID
// check never found the row the first call created, and the plain insert
// crashed the whole match with a raw "UNIQUE constraint failed:
// albums.mbid" error instead of just reusing the album that mbid already
// belongs to.
func TestGetOrCreateAlbumRecoversFromMBIDCollisionAcrossReleaseGroups(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("va-mbid", "Various Artists", "Various Artists")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}

	a1, err := db.GetOrCreateAlbum(artist.ID, "shared-release-mbid", "rg-one", "Cities 97 Sampler, Volume 7", "1995", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (first track): %v", err)
	}

	// Same physical release (mbid), but a different release-group MBID —
	// getAlbumByReleaseGroupMBID(artist.ID, "rg-two") finds nothing, so
	// this must fall through to the insert path and collide on mbid.
	a2, err := db.GetOrCreateAlbum(artist.ID, "shared-release-mbid", "rg-two", "Cities 97 Sampler, Volume 7", "1995", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum (second track, colliding mbid): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row instead of reusing the one that already owns this mbid: ID = %d, want %d", a2.ID, a1.ID)
	}
	if a2.ReleaseGroupMBID != "rg-one" {
		t.Errorf("ReleaseGroupMBID = %q, want the first call's rg-one preserved as-is", a2.ReleaseGroupMBID)
	}
}

func TestSetAlbumDescription(t *testing.T) {
	db := newTestStore(t)
	artist, err := db.GetOrCreateArtist("artist-mbid", "Avantasia", "Avantasia")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Moonglow", "2019", "Album")
	if err != nil {
		t.Fatal(err)
	}
	if album.DescriptionFetchedAt != nil {
		t.Errorf("DescriptionFetchedAt = %v, want nil before it's ever been fetched", album.DescriptionFetchedAt)
	}

	now := time.Now().UTC()
	if err := db.SetAlbumDescription(album.ID, "A metal opera concept album.", "Epic", now); err != nil {
		t.Fatalf("SetAlbumDescription: %v", err)
	}
	got, err := db.GetAlbum(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "A metal opera concept album." || got.Mood != "Epic" || got.DescriptionFetchedAt == nil {
		t.Errorf("got = %+v", got)
	}
}

func TestGetAlbumNotFound(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.GetAlbum(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListAlbumsByArtistEmptyIsNotNil guards against a real bug: a nil
// slice marshals to JSON null, not [], which would crash a web UI doing
// .length/.map() on an empty result.
func TestListAlbumsByArtistEmptyIsNotNil(t *testing.T) {
	db := newTestStore(t)
	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	list, err := db.ListAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatalf("ListAlbumsByArtist: %v", err)
	}
	if list == nil {
		t.Error("ListAlbumsByArtist returned nil for an empty result, want a non-nil empty slice")
	}
}
