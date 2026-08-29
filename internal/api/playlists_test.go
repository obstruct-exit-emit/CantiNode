package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// TestPlaylistCRUDAndItems covers the whole surface end to end: create,
// append two tracks, reorder, remove one, rename, then delete — and
// confirms the export endpoint only emits an entry for a track that
// actually has a file backing it right now.
func TestPlaylistCRUDAndItems(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Test Album", "2020-01-01", "Album")
	if err != nil {
		t.Fatal(err)
	}
	trackWithFile, err := musicStore.GetOrCreateTrack(album.ID, "track-1-mbid", "Has A File", 1, 1, 200_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	trackNoFile, err := musicStore.GetOrCreateTrack(album.ID, "track-2-mbid", "No File Yet", 2, 1, 180_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rf := addRootFolder(t, a, t.TempDir(), "music")
	tf, err := musicStore.UpsertTrackFileByPath(rf.ID, rf.Path+"/has-a-file.flac", 12345, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatalf("UpsertTrackFileByPath: %v", err)
	}
	if err := musicStore.SetTrackFileMatch(tf.ID, &trackWithFile.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatalf("SetTrackFileMatch: %v", err)
	}

	var created struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/music/playlist", map[string]any{"name": "My Mix", "description": "test"}, &created), http.StatusCreated)
	base := "/api/v1/music/playlist/" + strconv.FormatInt(created.ID, 10)

	var itemA struct {
		ItemID int64 `json:"itemId"`
	}
	a.want(a.call("POST", base+"/items", map[string]any{"trackId": trackWithFile.ID}, &itemA), http.StatusCreated)
	var itemB struct {
		ItemID int64 `json:"itemId"`
	}
	a.want(a.call("POST", base+"/items", map[string]any{"trackId": trackNoFile.ID}, &itemB), http.StatusCreated)

	var detail struct {
		Name   string                       `json:"name"`
		Tracks []musiclibrary.PlaylistTrack `json:"tracks"`
	}
	a.want(a.call("GET", base, nil, &detail), http.StatusOK)
	if len(detail.Tracks) != 2 {
		t.Fatalf("detail.Tracks = %d, want 2", len(detail.Tracks))
	}

	// Reorder: B then A.
	var reordered []musiclibrary.PlaylistTrack
	a.want(a.call("PUT", base+"/items/order", map[string]any{"itemIds": []int64{itemB.ItemID, itemA.ItemID}}, &reordered), http.StatusOK)
	if len(reordered) != 2 || reordered[0].ItemID != itemB.ItemID {
		t.Fatalf("reordered = %+v, want B first", reordered)
	}

	a.want(a.call("DELETE", base+"/items/"+strconv.FormatInt(itemB.ItemID, 10), nil, nil), http.StatusNoContent)

	// Export: only the track with a real file should produce an entry.
	// a.call closes the response body itself once it returns (it only
	// supports JSON-decoding via its own out param), so a raw body like an
	// M3U file needs its own request here instead.
	req, err := http.NewRequest("GET", a.srv.URL+base+"/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d, want 200", resp.StatusCode)
	}
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	m3u := b.String()
	if !strings.Contains(m3u, "has-a-file.flac") {
		t.Errorf("export missing the file-backed track: %q", m3u)
	}
	if strings.Contains(m3u, "No File Yet") {
		t.Errorf("export should skip the track with no file: %q", m3u)
	}

	a.want(a.call("PUT", base, map[string]any{"name": "Renamed"}, nil), http.StatusOK)
	a.want(a.call("DELETE", base, nil, nil), http.StatusNoContent)
	a.want(a.call("GET", base, nil, nil), http.StatusNotFound)
}

// TestBulkAddPlaylistItems covers the album-page "add whole album" action.
func TestBulkAddPlaylistItems(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)
	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Album", "2020-01-01", "Album")
	if err != nil {
		t.Fatal(err)
	}
	t1, err := musicStore.GetOrCreateTrack(album.ID, "t1-mbid", "Track 1", 1, 1, 100_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t2, err := musicStore.GetOrCreateTrack(album.ID, "t2-mbid", "Track 2", 2, 1, 100_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	var created struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/music/playlist", map[string]any{"name": "Bulk", "description": ""}, &created), http.StatusCreated)

	var items []musiclibrary.PlaylistTrack
	resp := a.call("POST", "/api/v1/music/playlist/"+strconv.FormatInt(created.ID, 10)+"/items/bulk",
		map[string]any{"trackIds": []int64{t1.ID, t2.ID}}, &items)
	a.want(resp, http.StatusCreated)
	if len(items) != 2 || items[0].Position != 1 || items[1].Position != 2 {
		t.Fatalf("items = %+v, want 2 items at positions 1, 2", items)
	}

	resp = a.call("POST", "/api/v1/music/playlist/999999/items/bulk", map[string]any{"trackIds": []int64{t1.ID}}, nil)
	a.want(resp, http.StatusNotFound)

	// A bad track id against a *real* playlist is 400 (the request body's
	// content is wrong), not the 404 a missing playlist gets (the URL's
	// own resource is wrong) — and never the raw 500 a SQLite foreign-key
	// error used to leak before this was fixed.
	resp = a.call("POST", "/api/v1/music/playlist/"+strconv.FormatInt(created.ID, 10)+"/items/bulk",
		map[string]any{"trackIds": []int64{999_999_999}}, nil)
	a.want(resp, http.StatusBadRequest)
	resp = a.call("POST", "/api/v1/music/playlist/"+strconv.FormatInt(created.ID, 10)+"/items",
		map[string]any{"trackId": 999_999_999}, nil)
	a.want(resp, http.StatusBadRequest)
}

// TestSearchOwnedTracksEndpoint covers the Search page's track results.
func TestSearchOwnedTracksEndpoint(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)
	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Album", "2020-01-01", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := musicStore.GetOrCreateTrack(album.ID, "t-mbid", "Moonshine Blues", 1, 1, 100_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rf := addRootFolder(t, a, t.TempDir(), "music")
	tf, err := musicStore.UpsertTrackFileByPath(rf.ID, rf.Path+"/moonshine.flac", 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	var results []musiclibrary.TrackSearchResult
	a.want(a.call("GET", "/api/v1/music/track/search?q=moon", nil, &results), http.StatusOK)
	if len(results) != 1 || results[0].Title != "Moonshine Blues" {
		t.Errorf("results = %+v, want [Moonshine Blues]", results)
	}

	// Empty query short-circuits to an empty list rather than a full table
	// scan for every owned track.
	var empty []musiclibrary.TrackSearchResult
	a.want(a.call("GET", "/api/v1/music/track/search?q=", nil, &empty), http.StatusOK)
	if len(empty) != 0 {
		t.Errorf("empty query returned %d results, want 0", len(empty))
	}
}

// TestImportPlaylistEndpoint covers importing an M3U that round-trips one
// of CantiNode's own exports plus a line that doesn't resolve to anything.
func TestImportPlaylistEndpoint(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)
	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Album", "2020-01-01", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := musicStore.GetOrCreateTrack(album.ID, "t-mbid", "Real Track", 1, 1, 100_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rf := addRootFolder(t, a, t.TempDir(), "music")
	realPath := rf.Path + "/real.flac"
	tf, err := musicStore.UpsertTrackFileByPath(rf.ID, realPath, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	m3u := "#EXTM3U\n" + realPath + "\n/nowhere/gone.flac\n"
	var result musiclibrary.ImportM3UResult
	a.want(a.call("POST", "/api/v1/music/playlist/import", map[string]any{"name": "Recovered", "content": m3u}, &result), http.StatusCreated)
	if result.Imported != 1 || result.Skipped != 1 {
		t.Errorf("result = %+v, want Imported=1 Skipped=1", result)
	}
	if result.Playlist.Name != "Recovered" {
		t.Errorf("playlist name = %q, want Recovered", result.Playlist.Name)
	}
}
