package musiclibrary

import (
	"testing"
	"time"
)

// TestListTrackFilesByStatusEmptyIsNotNil guards against a real bug: a nil
// slice marshals to JSON `null`, not `[]`, which would crash a web UI
// doing `.length`/`.map()` on the response of the unmatched-files
// endpoint on a fresh install with nothing scanned yet.
func TestListTrackFilesByStatusEmptyIsNotNil(t *testing.T) {
	db := newTestStore(t)
	list, err := db.ListTrackFilesByStatus(StatusUnmatched)
	if err != nil {
		t.Fatalf("ListTrackFilesByStatus: %v", err)
	}
	if list == nil {
		t.Error("ListTrackFilesByStatus returned nil for an empty result, want a non-nil empty slice")
	}
}

// TestListTrackFilesByIDs is the bulk counterpart to GetTrackFile — used by
// musicscanner's SuggestMatches to fetch a whole folder's worth of files in
// one query. A requested id with no matching row must simply be absent from
// the result, not an error, so the caller can still process the others.
func TestListTrackFilesByIDs(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)

	tf1, err := db.UpsertTrackFileByPath(rfID, "/music/one.mp3", 1000, "mp3", 320, 200000, `{}`)
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath 1: %v", err)
	}
	tf2, err := db.UpsertTrackFileByPath(rfID, "/music/two.mp3", 1000, "mp3", 320, 200000, `{}`)
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath 2: %v", err)
	}

	got, err := db.ListTrackFilesByIDs([]int64{tf1.ID, tf2.ID, 999999})
	if err != nil {
		t.Fatalf("ListTrackFilesByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got = %+v, want 2 (the missing id must be silently absent)", got)
	}
	if got[tf1.ID] == nil || got[tf1.ID].Path != "/music/one.mp3" {
		t.Errorf("got[tf1.ID] = %+v, want the /music/one.mp3 row", got[tf1.ID])
	}
	if got[tf2.ID] == nil || got[tf2.ID].Path != "/music/two.mp3" {
		t.Errorf("got[tf2.ID] = %+v, want the /music/two.mp3 row", got[tf2.ID])
	}
	if _, ok := got[999999]; ok {
		t.Error("got[999999] present, want absent")
	}
}

func TestListTrackFilesByIDsEmptyInput(t *testing.T) {
	db := newTestStore(t)
	got, err := db.ListTrackFilesByIDs(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ListTrackFilesByIDs(nil) = %v, %v, want empty map, nil", got, err)
	}
}

func TestUpsertTrackFileByPathInsertsThenUpdatesWithoutTouchingMatch(t *testing.T) {
	db := newTestStore(t)

	rfID := testMusicRoot(t, db)

	tf, err := db.UpsertTrackFileByPath(rfID, "/music/song.mp3", 1000, "mp3", 320, 200000, `{"artist":"X"}`)
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath (insert): %v", err)
	}
	if tf.MatchStatus != StatusUnmatched {
		t.Errorf("MatchStatus = %q, want unmatched", tf.MatchStatus)
	}

	artist, _ := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	album, _ := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	track, _ := db.GetOrCreateTrack(album.ID, "t-mbid", "Song", 1, 1, 200000)
	if err := db.SetTrackFileMatch(tf.ID, &track.ID, StatusMatched, 0.95); err != nil {
		t.Fatalf("SetTrackFileMatch: %v", err)
	}

	// Re-upsert (simulating a rescan) must not clear the match.
	tf2, err := db.UpsertTrackFileByPath(rfID, "/music/song.mp3", 1000, "mp3", 320, 200000, `{"artist":"X"}`)
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath (update): %v", err)
	}
	if tf2.ID != tf.ID {
		t.Fatalf("rescan created a new row: ID = %d, want %d", tf2.ID, tf.ID)
	}

	got, err := db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatalf("GetTrackFile: %v", err)
	}
	if got.MatchStatus != StatusMatched {
		t.Errorf("MatchStatus after rescan = %q, want matched (rescan must not clear a match)", got.MatchStatus)
	}
	if got.TrackID == nil || *got.TrackID != track.ID {
		t.Errorf("TrackID after rescan = %v, want %d", got.TrackID, track.ID)
	}
}

func TestListTrackFilesByStatus(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)

	unmatched, err := db.UpsertTrackFileByPath(rfID, "/music/a.mp3", 1, "mp3", 128, 1000, "{}")
	if err != nil {
		t.Fatal(err)
	}
	matched, err := db.UpsertTrackFileByPath(rfID, "/music/b.mp3", 1, "mp3", 128, 1000, "{}")
	if err != nil {
		t.Fatal(err)
	}
	artist, _ := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	album, _ := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	track, _ := db.GetOrCreateTrack(album.ID, "t-mbid", "Song", 1, 1, 1000)
	if err := db.SetTrackFileMatch(matched.ID, &track.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListTrackFilesByStatus(StatusUnmatched)
	if err != nil {
		t.Fatalf("ListTrackFilesByStatus: %v", err)
	}
	if len(list) != 1 || list[0].ID != unmatched.ID {
		t.Errorf("unmatched list = %+v, want just %d", list, unmatched.ID)
	}

	matchedList, err := db.ListTrackFilesByStatus(StatusMatched)
	if err != nil {
		t.Fatalf("ListTrackFilesByStatus: %v", err)
	}
	if len(matchedList) != 1 || matchedList[0].ID != matched.ID {
		t.Errorf("matched list = %+v, want just %d", matchedList, matched.ID)
	}
}

func TestSetTrackFileOrganized(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)
	tf, _ := db.UpsertTrackFileByPath(rfID, "/music/a.mp3", 1, "mp3", 128, 1000, "{}")

	now := time.Now().UTC()
	if err := db.SetTrackFileOrganized(tf.ID, "/music/Artist/Album/01 - Song.mp3", now); err != nil {
		t.Fatalf("SetTrackFileOrganized: %v", err)
	}

	got, err := db.GetTrackFile(tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/music/Artist/Album/01 - Song.mp3" {
		t.Errorf("Path = %q", got.Path)
	}
	if got.OrganizedAt == nil {
		t.Error("OrganizedAt should be set")
	}
}

func TestDeleteTrackFilesMissing(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)

	keep, _ := db.UpsertTrackFileByPath(rfID, "/music/keep.mp3", 1, "mp3", 128, 1000, "{}")
	_, err := db.UpsertTrackFileByPath(rfID, "/music/gone.mp3", 1, "mp3", 128, 1000, "{}")
	if err != nil {
		t.Fatal(err)
	}

	removed, err := db.DeleteTrackFilesMissing(rfID, []string{"/music/keep.mp3"})
	if err != nil {
		t.Fatalf("DeleteTrackFilesMissing: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	list, err := db.ListTrackFilesByRootFolder(rfID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != keep.ID {
		t.Errorf("remaining list = %+v, want just %d", list, keep.ID)
	}
}

func TestListTrackFilesByArtist(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)

	artist, _ := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	album, _ := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	track, _ := db.GetOrCreateTrack(album.ID, "t-mbid", "Song", 1, 1, 1000)

	mine, err := db.UpsertTrackFileByPath(rfID, "/music/mine.mp3", 1, "mp3", 128, 1000, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTrackFileMatch(mine.ID, &track.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}

	// A different artist's file must not show up.
	other, _ := db.GetOrCreateArtist("b-mbid", "Other", "Other")
	otherAlbum, _ := db.GetOrCreateAlbum(other.ID, "al2-mbid", "rg2-mbid", "Other Album", "2021", "Album")
	otherTrack, _ := db.GetOrCreateTrack(otherAlbum.ID, "t2-mbid", "Other Song", 1, 1, 1000)
	otherFile, err := db.UpsertTrackFileByPath(rfID, "/music/other.mp3", 1, "mp3", 128, 1000, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTrackFileMatch(otherFile.ID, &otherTrack.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}

	// An unmatched file (no track_id) never matches the join, so it's
	// correctly absent regardless of artist.
	if _, err := db.UpsertTrackFileByPath(rfID, "/music/unmatched.mp3", 1, "mp3", 128, 1000, "{}"); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListTrackFilesByArtist(artist.ID)
	if err != nil {
		t.Fatalf("ListTrackFilesByArtist: %v", err)
	}
	if len(list) != 1 || list[0].ID != mine.ID {
		t.Errorf("list = %+v, want just %d", list, mine.ID)
	}
}

func TestListArtistsAndAlbumsOnlyShowMatchedContent(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := db.GetOrCreateTrack(album.ID, "t-mbid", "Song", 1, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}

	// An artist/album/track that exists (matched by some other file
	// elsewhere) but with no track_files row pointing at it yet shouldn't
	// show up in the browse lists.
	empty, err := db.ListArtists()
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("ListArtists with no track_files = %+v, want empty", empty)
	}

	rfID := testMusicRoot(t, db)
	tf, _ := db.UpsertTrackFileByPath(rfID, "/music/a.mp3", 1, "mp3", 128, 1000, "{}")
	if err := db.SetTrackFileMatch(tf.ID, &track.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}

	artists, err := db.ListArtists()
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 1 || artists[0].ID != artist.ID {
		t.Errorf("ListArtists = %+v, want just artist %d", artists, artist.ID)
	}

	albums, err := db.ListAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].ID != album.ID {
		t.Errorf("ListAlbumsByArtist = %+v, want just album %d", albums, album.ID)
	}
}

// TestCountOwnedAlbumsByArtist is the regression test for a real bug: the
// library grid's poster-card subtitle always read "0 albums" for every
// artist, even ones with real owned albums, because
// handleListMusicArtists never attached an owned-album count at all
// (only the single-artist detail endpoint did). CountOwnedAlbumsByArtist
// is the bulk query that backs the fix — same "owned" test
// ListAlbumsByArtist itself uses (a matched track file), computed for
// every artist in one query.
func TestCountOwnedAlbumsByArtist(t *testing.T) {
	db := newTestStore(t)
	rfID := testMusicRoot(t, db)

	artistWithAlbum, err := db.GetOrCreateArtist("a-mbid", "Has Albums", "Has Albums")
	if err != nil {
		t.Fatal(err)
	}
	album1, err := db.GetOrCreateAlbum(artistWithAlbum.ID, "al-1-mbid", "rg-1-mbid", "Album One", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	album2, err := db.GetOrCreateAlbum(artistWithAlbum.ID, "al-2-mbid", "rg-2-mbid", "Album Two", "2021", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track1, err := db.GetOrCreateTrack(album1.ID, "t-1-mbid", "Song One", 1, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	track2, err := db.GetOrCreateTrack(album2.ID, "t-2-mbid", "Song Two", 1, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	tf1, _ := db.UpsertTrackFileByPath(rfID, "/music/a1.mp3", 1, "mp3", 128, 1000, "{}")
	if err := db.SetTrackFileMatch(tf1.ID, &track1.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}
	tf2, _ := db.UpsertTrackFileByPath(rfID, "/music/a2.mp3", 1, "mp3", 128, 1000, "{}")
	if err := db.SetTrackFileMatch(tf2.ID, &track2.ID, StatusMatched, 0.9); err != nil {
		t.Fatal(err)
	}

	// An artist that's monitored but owns nothing yet — must not show up
	// in the owned-count map at all (a missing key, not a zero entry).
	unowned, err := db.GetOrCreateArtist("unowned-mbid", "Nothing Owned", "Nothing Owned")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetArtistMonitored(unowned.ID, true); err != nil {
		t.Fatal(err)
	}

	counts, err := db.CountOwnedAlbumsByArtist()
	if err != nil {
		t.Fatalf("CountOwnedAlbumsByArtist: %v", err)
	}
	if counts[artistWithAlbum.ID] != 2 {
		t.Errorf("counts[%d] = %d, want 2", artistWithAlbum.ID, counts[artistWithAlbum.ID])
	}
	if _, ok := counts[unowned.ID]; ok {
		t.Errorf("counts[%d] = %d, want no entry at all", unowned.ID, counts[unowned.ID])
	}
}

func TestCountReleaseGroupsByArtist(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceArtistReleaseGroups(artist.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Album One"},
		{ReleaseGroupMBID: "rg-2", Title: "Album Two"},
		{ReleaseGroupMBID: "rg-3", Title: "Album Three"},
	}); err != nil {
		t.Fatal(err)
	}

	counts, err := db.CountReleaseGroupsByArtist()
	if err != nil {
		t.Fatalf("CountReleaseGroupsByArtist: %v", err)
	}
	if counts[artist.ID] != 3 {
		t.Errorf("counts[%d] = %d, want 3", artist.ID, counts[artist.ID])
	}
}

// TestCountWantedAlbumsByArtist confirms the count backing the library
// grid's "owned/total" subtitle counts only wanted_albums rows, not the
// artist's entire cached discography — an artist with a big catalog and
// nothing wanted beyond what's owned should read as a small total, not a
// number reflecting every release group Missing would otherwise list.
func TestCountWantedAlbumsByArtist(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	// A big cached discography the artist doesn't actually want any more
	// of yet — must not count toward "wanted".
	if err := db.ReplaceArtistReleaseGroups(artist.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Album One"},
		{ReleaseGroupMBID: "rg-2", Title: "Album Two"},
		{ReleaseGroupMBID: "rg-3", Title: "Album Three"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetOrCreateWantedAlbum(artist.ID, "rg-2", "Album Two", "Album", "2021"); err != nil {
		t.Fatal(err)
	}

	counts, err := db.CountWantedAlbumsByArtist()
	if err != nil {
		t.Fatalf("CountWantedAlbumsByArtist: %v", err)
	}
	if counts[artist.ID] != 1 {
		t.Errorf("counts[%d] = %d, want 1 (only the actually-wanted album, not the whole discography)", artist.ID, counts[artist.ID])
	}
}
