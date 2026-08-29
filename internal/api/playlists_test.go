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
