package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// TestCancelGrabResolvesStuckPendingGrab covers the manual escape hatch for a
// grab that's stuck reporting "pending" forever — its queue entry already
// gone (e.g. a torrent grab from before the client-item-id fix, or one
// removed straight from the client), with no matching queue item left for
// removeQueueItem to resolve it against. Cancelling by grab id directly must
// work regardless.
func TestCancelGrabResolvesStuckPendingGrab(t *testing.T) {
	a := newTestAPI(t)
	store := download.NewStore(a.db)

	grab := &download.GrabRecord{
		Title: "Dune Messiah", Protocol: "torrent", MediaType: "music",
	}
	if err := store.AddGrab(grab); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}

	resp := a.call("POST", "/api/v1/grab/"+strconv.FormatInt(grab.ID, 10)+"/cancel", nil, nil)
	a.want(resp, http.StatusOK)

	grabs, err := store.ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grabs {
		if g.ID == grab.ID {
			t.Errorf("grab %d still reports status %q after cancel, want it resolved", g.ID, g.Status)
		}
	}
}

func TestCancelGrabNotFound(t *testing.T) {
	a := newTestAPI(t)
	resp := a.call("POST", "/api/v1/grab/999999/cancel", nil, nil)
	a.want(resp, http.StatusNotFound)
}

// mockSabForRemove fakes just enough of SABnzbd's API for
// handleRemoveQueueItem to succeed: queue/history delete both return ok.
func mockSabForRemove(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status": true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRemoveQueueItemMatchesItemIDCaseInsensitivelyAndRevertsWanted is the
// regression case for two real bugs found together: a magnet's info hash is
// stored lowercase (download.magnetHash), but a debrid bridge routinely
// echoes it back in a different case, so a straight string comparison here
// never resolves the grab it belongs to; and even when it does resolve,
// nothing reverted the wanted album back to "wanted", leaving it stuck at
// "downloading" forever with no way to try a different release.
func TestRemoveQueueItemMatchesItemIDCaseInsensitivelyAndRevertsWanted(t *testing.T) {
	a := newTestAPI(t)
	sab := mockSabForRemove(t)

	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "Sabnzb", "type": "sabnzbd", "host": sab.URL, "apiKey": "key", "enabled": true,
	}, nil), http.StatusCreated)

	musicStore := musiclibrary.NewStore(a.db)
	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatalf("set wanted album downloading: %v", err)
	}

	store := download.NewStore(a.db)
	grab := &download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "ABC123",
		Title: "Test Album", Protocol: "usenet", MediaType: "music",
	}
	if err := store.AddGrab(grab); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}

	// The client item id in the URL is deliberately lowercased relative to
	// what was stored, mirroring a bridge reporting a hash in a different
	// case than the magnet it came from.
	resp := a.call("DELETE", fmt.Sprintf("/api/v1/queue/1/%s", "abc123"), nil, nil)
	a.want(resp, http.StatusOK)

	grabs, err := store.ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grabs {
		if g.ID == grab.ID {
			t.Errorf("grab %d still reports status %q after removal, want it resolved despite the case mismatch", g.ID, g.Status)
		}
	}

	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want %q (reverted after the grab was removed)", got.Status, musiclibrary.WantedStatusWanted)
	}
}
