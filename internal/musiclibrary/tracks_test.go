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

	t1, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack: %v", err)
	}
	if t1.AlbumID != album.ID {
		t.Errorf("AlbumID = %d, want %d", t1.AlbumID, album.ID)
	}

	t2, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Alpha and Omega", 3, 1, 202000, "", "", "")
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

// TestGetOrCreateTrackRefreshesArtistCreditOnExistingRow is the regression
// test for a real live gap: a track matched before artist_credit_mbid
// existed (or before applyMatch correctly resolved it) stayed wrong
// forever, because GetOrCreateTrack's existing-row path never updated
// artist_credit/artist_credit_mbid once a track row was first created —
// found live re-matching a real Various Artists track: the corrected ID
// never took effect on the already-existing row. Only artist_credit/
// artist_credit_mbid refresh this way; every other field (checked below)
// stays exactly as first inserted, since those describe the recording
// itself and have no reason to legitimately change between calls.
func TestGetOrCreateTrackRefreshesArtistCreditOnExistingRow(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("va-mbid", "Various Artists", "Various Artists")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Compilation", "1998", "Compilation")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}

	// First match: as if artist_credit_mbid didn't resolve correctly yet
	// (the pre-fix behavior) — stored empty.
	t1, err := db.GetOrCreateTrack(album.ID, "track-mbid", "In the Air Tonight", 1, 1, 200000, "Phil Collins", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (first match): %v", err)
	}
	if t1.ArtistCreditMBID != "" {
		t.Fatalf("ArtistCreditMBID = %q, want empty on first insert", t1.ArtistCreditMBID)
	}

	// Re-match with the now-correctly-resolved MBID — same album/mbid, so
	// this hits the existing row, not a fresh insert.
	t2, err := db.GetOrCreateTrack(album.ID, "track-mbid", "In the Air Tonight", 1, 1, 200000, "Phil Collins", "phil-collins-mbid", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (re-match): %v", err)
	}
	if t2.ID != t1.ID {
		t.Fatalf("re-match created a new row: ID = %d, want %d", t2.ID, t1.ID)
	}
	if t2.ArtistCreditMBID != "phil-collins-mbid" {
		t.Errorf("ArtistCreditMBID after re-match = %q, want phil-collins-mbid", t2.ArtistCreditMBID)
	}

	stored, err := db.GetTrack(t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ArtistCreditMBID != "phil-collins-mbid" {
		t.Errorf("stored ArtistCreditMBID = %q, want phil-collins-mbid (the refresh must actually persist, not just the returned value)", stored.ArtistCreditMBID)
	}
	if stored.Title != "In the Air Tonight" || stored.TrackNumber != 1 || stored.DiscNumber != 1 || stored.DurationMs != 200000 {
		t.Errorf("non-credit fields changed on refresh: %+v", stored)
	}
}

// TestGetOrCreateTrackComposerUpgradesButNeverBlanks covers composer's
// asymmetric refresh rule (see GetOrCreateTrack's own doc comment): unlike
// artist_credit/artist_credit_mbid, an empty composer on a later call means
// "this match path had no relationship data" (e.g. the batched recording-
// search path), not "confirmed no composer" — a re-match must be able to
// upgrade a blank composer to a real one, but never regress a real one back
// to blank.
func TestGetOrCreateTrackComposerUpgradesButNeverBlanks(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("artist-mbid", "Jeff Buckley", "Buckley, Jeff")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Grace", "1994", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}

	// First match via a path with no relationship data (e.g. the batched
	// recording-search path) — composer stored blank.
	t1, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Hallelujah", 1, 1, 400000, "", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (first match): %v", err)
	}
	if t1.Composer != "" {
		t.Fatalf("Composer = %q, want empty on first insert", t1.Composer)
	}

	// Re-match via a richer path that resolved the real composer — must
	// upgrade the existing row.
	t2, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Hallelujah", 1, 1, 400000, "", "", "Leonard Cohen")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (re-match with composer): %v", err)
	}
	if t2.Composer != "Leonard Cohen" {
		t.Errorf("Composer after upgrade = %q, want Leonard Cohen", t2.Composer)
	}

	// A later re-match via a path with no relationship data again must NOT
	// blank out the composer already resolved.
	t3, err := db.GetOrCreateTrack(album.ID, "track-mbid", "Hallelujah", 1, 1, 400000, "", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (re-match with no composer data): %v", err)
	}
	if t3.Composer != "Leonard Cohen" {
		t.Errorf("Composer after a blank re-match = %q, want it to keep Leonard Cohen, not regress to blank", t3.Composer)
	}

	stored, err := db.GetTrack(t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Composer != "Leonard Cohen" {
		t.Errorf("stored Composer = %q, want Leonard Cohen (the upgrade must actually persist)", stored.Composer)
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

	albumTrack, err := db.GetOrCreateTrack(album.ID, "rec-change", "Change", 6, 1, 220000, "", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack (album): %v", err)
	}
	singleTrack, err := db.GetOrCreateTrack(single.ID, "rec-change", "Change", 1, 1, 220000, "", "", "")
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
	again, err := db.GetOrCreateTrack(album.ID, "rec-change", "Change", 6, 1, 220000, "", "", "")
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
