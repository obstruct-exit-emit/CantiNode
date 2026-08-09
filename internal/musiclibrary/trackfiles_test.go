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
