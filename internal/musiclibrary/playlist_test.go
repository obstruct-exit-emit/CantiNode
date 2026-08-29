package musiclibrary

import (
	"errors"
	"testing"
)

// seedTrack creates a minimal artist/album/track chain for playlist tests.
func seedTrack(t *testing.T, db *Store, artistName, albumTitle, trackTitle string, durationMs int64) *Track {
	t.Helper()
	artist, err := db.GetOrCreateArtist(artistName+"-mbid", artistName, artistName)
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, albumTitle+"-mbid", albumTitle+"-rg-mbid", albumTitle, "2020-01-01", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	track, err := db.GetOrCreateTrack(album.ID, trackTitle+"-mbid", trackTitle, 1, 1, durationMs, "", "", "")
	if err != nil {
		t.Fatalf("GetOrCreateTrack: %v", err)
	}
	return track
}

func TestPlaylistCreateAppendReorderRemove(t *testing.T) {
	db := newTestStore(t)

	p, err := db.CreatePlaylist("Road Trip", "songs for the drive")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if p.TrackCount != 0 {
		t.Errorf("new playlist TrackCount = %d, want 0", p.TrackCount)
	}

	trackA := seedTrack(t, db, "Artist A", "Album A", "Song A", 200_000)
	trackB := seedTrack(t, db, "Artist B", "Album B", "Song B", 180_000)

	itemA, err := db.AppendPlaylistItem(p.ID, trackA.ID)
	if err != nil {
		t.Fatalf("AppendPlaylistItem A: %v", err)
	}
	itemB, err := db.AppendPlaylistItem(p.ID, trackB.ID)
	if err != nil {
		t.Fatalf("AppendPlaylistItem B: %v", err)
	}
	if itemA.Position != 1 || itemB.Position != 2 {
		t.Errorf("positions = %d, %d, want 1, 2", itemA.Position, itemB.Position)
	}
	if itemA.ArtistName != "Artist A" || itemA.AlbumTitle != "Album A" {
		t.Errorf("itemA join = %+v, want Artist A / Album A", itemA)
	}
	if itemA.TrackFileID != 0 {
		t.Errorf("itemA TrackFileID = %d, want 0 (no file matched to this track)", itemA.TrackFileID)
	}

	got, err := db.GetPlaylist(p.ID)
	if err != nil {
		t.Fatalf("GetPlaylist: %v", err)
	}
	if got.TrackCount != 2 || got.TotalDurationMs != 380_000 {
		t.Errorf("GetPlaylist after 2 appends = %+v, want TrackCount=2 TotalDurationMs=380000", got)
	}

	// Reorder: B before A.
	if err := db.ReorderPlaylistItems(p.ID, []int64{itemB.ItemID, itemA.ItemID}); err != nil {
		t.Fatalf("ReorderPlaylistItems: %v", err)
	}
	tracks, err := db.ListPlaylistTracks(p.ID)
	if err != nil {
		t.Fatalf("ListPlaylistTracks: %v", err)
	}
	if len(tracks) != 2 || tracks[0].ItemID != itemB.ItemID || tracks[1].ItemID != itemA.ItemID {
		t.Fatalf("order after reorder = %+v, want [B, A]", tracks)
	}

	// Reordering an item id that isn't actually in this playlist must fail
	// outright, not silently reassign someone else's item.
	other, err := db.CreatePlaylist("Other", "")
	if err != nil {
		t.Fatalf("CreatePlaylist other: %v", err)
	}
	if err := db.ReorderPlaylistItems(other.ID, []int64{itemA.ItemID}); err == nil {
		t.Error("ReorderPlaylistItems accepted an item id from a different playlist")
	}

	if err := db.RemovePlaylistItem(p.ID, itemB.ItemID); err != nil {
		t.Fatalf("RemovePlaylistItem: %v", err)
	}
	tracks, err = db.ListPlaylistTracks(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].ItemID != itemA.ItemID {
		t.Fatalf("tracks after remove = %+v, want just [A]", tracks)
	}

	if err := db.DeletePlaylist(p.ID); err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}
	if _, err := db.GetPlaylist(p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPlaylist after delete: err = %v, want ErrNotFound", err)
	}
	// Cascade: the playlist's items go with it, not left as orphaned rows.
	var orphaned int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM playlist_items WHERE playlist_id = ?`, p.ID).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	if orphaned != 0 {
		t.Errorf("playlist_items rows left after DeletePlaylist = %d, want 0", orphaned)
	}
}

// TestReorderPlaylistItemsRejectsPartialList covers a stale or racing
// reorder request — e.g. two browser tabs on the same playlist, or a
// drag-and-drop reorder submitted just as another request removed an item.
// A reorder that doesn't name every item currently in the playlist must be
// rejected outright rather than silently leaving the missing item's old
// position value in place, which can collide with a position the request
// just assigned to a different item and leave the playlist's order
// unstable (ties broken arbitrarily by SQLite, not by any real intent).
func TestReorderPlaylistItemsRejectsPartialList(t *testing.T) {
	db := newTestStore(t)
	p, err := db.CreatePlaylist("Partial Reorder", "")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	trackA := seedTrack(t, db, "Artist A", "Album A", "Song A", 200_000)
	trackB := seedTrack(t, db, "Artist B", "Album B", "Song B", 180_000)
	trackC := seedTrack(t, db, "Artist C", "Album C", "Song C", 150_000)
	itemA, err := db.AppendPlaylistItem(p.ID, trackA.ID)
	if err != nil {
		t.Fatalf("append A: %v", err)
	}
	itemB, err := db.AppendPlaylistItem(p.ID, trackB.ID)
	if err != nil {
		t.Fatalf("append B: %v", err)
	}
	if _, err := db.AppendPlaylistItem(p.ID, trackC.ID); err != nil {
		t.Fatalf("append C: %v", err)
	}

	// Only A and B named — C, still in the playlist, is left out.
	if err := db.ReorderPlaylistItems(p.ID, []int64{itemB.ItemID, itemA.ItemID}); err == nil {
		t.Error("ReorderPlaylistItems accepted a list missing an item still in the playlist")
	}

	// Nothing should have moved: nothing done to any item's neighbors
	// deserves to be persisted for the accepted-outright case anyway,
	// but here the whole point is that positions must still be distinct.
	var collisions int
	if err := db.db.QueryRow(
		`SELECT COUNT(*) FROM (SELECT position FROM playlist_items WHERE playlist_id = ? GROUP BY position HAVING COUNT(*) > 1)`,
		p.ID,
	).Scan(&collisions); err != nil {
		t.Fatal(err)
	}
	if collisions != 0 {
		t.Errorf("playlist has %d colliding position value(s) after a rejected partial reorder", collisions)
	}
}

func TestAppendPlaylistItemNotFound(t *testing.T) {
	db := newTestStore(t)
	track := seedTrack(t, db, "Artist", "Album", "Song", 100_000)
	if _, err := db.AppendPlaylistItem(999_999, track.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendPlaylistItem on missing playlist: err = %v, want ErrNotFound", err)
	}
}

// TestAppendPlaylistItemBadTrackID is the regression case for a real bug
// found live: appending a track id that doesn't exist used to fall
// straight through to SQLite's own foreign-key-constraint error — a raw,
// unhandled 500 at the API layer — instead of a clean, distinguishable
// error a handler can map to 400. Covers both the single and bulk append
// paths, and confirms a bulk call with one bad id among good ones inserts
// nothing at all (the transaction rolls back) rather than partially
// succeeding.
func TestAppendPlaylistItemBadTrackID(t *testing.T) {
	db := newTestStore(t)
	p, err := db.CreatePlaylist("Bad Track Test", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.AppendPlaylistItem(p.ID, 999_999_999); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("AppendPlaylistItem with a bad track id: err = %v, want ErrTrackNotFound", err)
	}

	good := seedTrack(t, db, "Artist", "Album", "Song", 100_000)
	if _, err := db.AppendPlaylistItems(p.ID, []int64{good.ID, 999_999_999}); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("AppendPlaylistItems with one bad id among good ones: err = %v, want ErrTrackNotFound", err)
	}

	// The good id must not have been left inserted by the failed bulk
	// call — the whole batch is one transaction.
	got, err := db.GetPlaylist(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrackCount != 0 {
		t.Errorf("TrackCount after a failed bulk append = %d, want 0 (transaction should have rolled back)", got.TrackCount)
	}
}

// seedTrackWithFile is seedTrack plus a real track_files row backing it,
// at the given path — needed for anything that resolves a path back to a
// track (M3U import) or requires a "currently playable" track (search).
func seedTrackWithFile(t *testing.T, db *Store, artistName, albumTitle, trackTitle, path string, durationMs int64) *Track {
	t.Helper()
	track := seedTrack(t, db, artistName, albumTitle, trackTitle, durationMs)
	var rootFolderID int64
	if err := db.db.QueryRow(`SELECT id FROM root_folders LIMIT 1`).Scan(&rootFolderID); err != nil {
		res, err := db.db.Exec(`INSERT INTO root_folders (name, media_type, path, is_default) VALUES ('test', 'music', 'C:/music', 1)`)
		if err != nil {
			t.Fatalf("seed root folder: %v", err)
		}
		rootFolderID, err = res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
	}
	tf, err := db.UpsertTrackFileByPath(rootFolderID, path, 12345, "flac", 0, durationMs, "{}")
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath: %v", err)
	}
	if err := db.SetTrackFileMatch(tf.ID, &track.ID, StatusMatched, 1.0); err != nil {
		t.Fatalf("SetTrackFileMatch: %v", err)
	}
	return track
}

func TestAppendPlaylistItemsBulk(t *testing.T) {
	db := newTestStore(t)
	p, err := db.CreatePlaylist("Album Dump", "")
	if err != nil {
		t.Fatal(err)
	}
	a := seedTrack(t, db, "Artist", "Album", "Track A", 100_000)
	b := seedTrack(t, db, "Artist", "Album", "Track B", 100_000)

	items, err := db.AppendPlaylistItems(p.ID, []int64{a.ID, b.ID})
	if err != nil {
		t.Fatalf("AppendPlaylistItems: %v", err)
	}
	if len(items) != 2 || items[0].Position != 1 || items[1].Position != 2 {
		t.Fatalf("items = %+v, want positions 1, 2", items)
	}

	got, err := db.GetPlaylist(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrackCount != 2 {
		t.Errorf("TrackCount = %d, want 2", got.TrackCount)
	}

	if _, err := db.AppendPlaylistItems(999_999, []int64{a.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendPlaylistItems on missing playlist: err = %v, want ErrNotFound", err)
	}
}

func TestImportPlaylistFromM3U(t *testing.T) {
	db := newTestStore(t)
	seedTrackWithFile(t, db, "Artist A", "Album A", "Song A", "C:/music/Artist A/Album A/01 Song A.flac", 200_000)
	seedTrackWithFile(t, db, "Artist B", "Album B", "Song B", "C:/music/Artist B/Album B/01 Song B.flac", 180_000)

	m3u := "#EXTM3U\n" +
		"#EXTINF:200,Artist A - Song A\n" +
		"C:/music/Artist A/Album A/01 Song A.flac\n" +
		"\n" +
		"C:/music/nonexistent/gone.flac\n" +
		"#EXTINF:180,Artist B - Song B\n" +
		"C:/music/Artist B/Album B/01 Song B.flac\n"

	result, err := db.ImportPlaylistFromM3U("Imported Mix", m3u)
	if err != nil {
		t.Fatalf("ImportPlaylistFromM3U: %v", err)
	}
	if result.Imported != 2 {
		t.Errorf("Imported = %d, want 2", result.Imported)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (the nonexistent path)", result.Skipped)
	}
	if result.Playlist.Name != "Imported Mix" || result.Playlist.TrackCount != 2 {
		t.Errorf("Playlist = %+v, want Name=Imported Mix TrackCount=2", result.Playlist)
	}

	tracks, err := db.ListPlaylistTracks(result.Playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].Title != "Song A" || tracks[1].Title != "Song B" {
		t.Fatalf("tracks = %+v, want [Song A, Song B] in file order", tracks)
	}
}

func TestSearchOwnedTracks(t *testing.T) {
	db := newTestStore(t)
	seedTrackWithFile(t, db, "Artist A", "Album A", "Moonlight Sonata", "C:/music/a.flac", 200_000)
	seedTrackWithFile(t, db, "Artist B", "Album B", "Moon River", "C:/music/b.flac", 180_000)
	seedTrack(t, db, "Artist C", "Album C", "Moon Without No Name", 150_000) // no file — must not appear

	got, err := db.SearchOwnedTracks("moon", 10)
	if err != nil {
		t.Fatalf("SearchOwnedTracks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (the two file-backed tracks), got %+v", len(got), got)
	}
	for _, r := range got {
		if r.TrackFileID == 0 {
			t.Errorf("result %+v has no TrackFileID", r)
		}
	}
}

// TestSearchOwnedTracksEscapesLikeWildcards covers a track title containing
// a literal '_' or '%' — both are SQL LIKE wildcards, and a search query
// built by simply splicing user input into the pattern treats them as such
// instead of literal characters, causing false-positive matches unrelated
// to what the user actually typed.
func TestSearchOwnedTracksEscapesLikeWildcards(t *testing.T) {
	db := newTestStore(t)
	seedTrackWithFile(t, db, "Artist A", "Album A", "Track_A", "C:/music/underscore.flac", 200_000)
	seedTrackWithFile(t, db, "Artist B", "Album B", "TrackXA", "C:/music/noliteral.flac", 180_000)

	got, err := db.SearchOwnedTracks("Track_A", 10)
	if err != nil {
		t.Fatalf("SearchOwnedTracks: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Track_A" {
		t.Errorf("SearchOwnedTracks(%q) = %+v, want only the literal match [Track_A] — '_' matched any character instead of itself", "Track_A", got)
	}
}

func TestTracksInAnyPlaylist(t *testing.T) {
	db := newTestStore(t)
	trackA := seedTrack(t, db, "Artist A", "Album A", "Song A", 200_000)
	trackB := seedTrack(t, db, "Artist B", "Album B", "Song B", 180_000)
	trackC := seedTrack(t, db, "Artist C", "Album C", "Song C", 150_000)

	p, err := db.CreatePlaylist("Some Playlist", "")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if _, err := db.AppendPlaylistItem(p.ID, trackA.ID); err != nil {
		t.Fatalf("AppendPlaylistItem: %v", err)
	}

	got, err := db.TracksInAnyPlaylist([]int64{trackA.ID, trackB.ID, trackC.ID})
	if err != nil {
		t.Fatalf("TracksInAnyPlaylist: %v", err)
	}
	if !got[trackA.ID] {
		t.Errorf("trackA is in a playlist but TracksInAnyPlaylist doesn't report it")
	}
	if got[trackB.ID] || got[trackC.ID] {
		t.Errorf("TracksInAnyPlaylist = %v, want only trackA present", got)
	}

	empty, err := db.TracksInAnyPlaylist(nil)
	if err != nil {
		t.Fatalf("TracksInAnyPlaylist(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("TracksInAnyPlaylist(nil) = %v, want empty", empty)
	}
}
