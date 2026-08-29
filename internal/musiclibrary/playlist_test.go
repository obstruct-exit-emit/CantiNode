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

func TestAppendPlaylistItemNotFound(t *testing.T) {
	db := newTestStore(t)
	track := seedTrack(t, db, "Artist", "Album", "Song", 100_000)
	if _, err := db.AppendPlaylistItem(999_999, track.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppendPlaylistItem on missing playlist: err = %v, want ErrNotFound", err)
	}
}
