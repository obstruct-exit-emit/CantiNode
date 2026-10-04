package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

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

// TestCancelGrabReleasesWantedAndUpgradeClaims is the regression case for
// a gap in handleCancelGrab found live alongside the same one just fixed in
// handleRemoveQueueItem: cancelling a grab only ever resolved the grab's
// own status, never released the claim it was holding — a wanted album
// left stuck at "downloading" forever, or (for an upgrade grab) an owned
// album's own upgrade_pending left set forever, with no way to try again
// short of a full server restart either way.
func TestCancelGrabReleasesWantedAndUpgradeClaims(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)
	store := download.NewStore(a.db)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}

	t.Run("wanted album", func(t *testing.T) {
		wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-wanted-mbid", "Wanted Album", "Album", "2020")
		if err != nil {
			t.Fatalf("seed wanted album: %v", err)
		}
		claimed, err := musicStore.ClaimWantedAlbumForDownload(wanted.ID)
		if err != nil || !claimed {
			t.Fatalf("claim should succeed on a freshly-seeded wanted album: claimed=%v err=%v", claimed, err)
		}
		grab := &download.GrabRecord{WantedAlbumID: wanted.ID, Title: "Wanted Album", Protocol: "torrent", MediaType: "music"}
		if err := store.AddGrab(grab); err != nil {
			t.Fatalf("AddGrab: %v", err)
		}

		a.want(a.call("POST", "/api/v1/grab/"+strconv.FormatInt(grab.ID, 10)+"/cancel", nil, nil), http.StatusOK)

		got, err := musicStore.GetWantedAlbum(wanted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != musiclibrary.WantedStatusWanted {
			t.Errorf("wanted album status = %q, want %q (reverted after cancel)", got.Status, musiclibrary.WantedStatusWanted)
		}
	})

	t.Run("upgrade album", func(t *testing.T) {
		album, err := musicStore.GetOrCreateAlbum(artist.ID, "al-upgrade-mbid", "rg-upgrade-mbid", "Upgrade Album", "2020", "Album")
		if err != nil {
			t.Fatalf("seed album: %v", err)
		}
		claimed, err := musicStore.ClaimAlbumForUpgrade(album.ID)
		if err != nil || !claimed {
			t.Fatalf("claim should succeed on a freshly-seeded album: claimed=%v err=%v", claimed, err)
		}
		grab := &download.GrabRecord{UpgradeAlbumID: album.ID, Title: "Upgrade Album", Protocol: "torrent", MediaType: "music"}
		if err := store.AddGrab(grab); err != nil {
			t.Fatalf("AddGrab: %v", err)
		}

		a.want(a.call("POST", "/api/v1/grab/"+strconv.FormatInt(grab.ID, 10)+"/cancel", nil, nil), http.StatusOK)

		stillClaimed, err := musicStore.ClaimAlbumForUpgrade(album.ID)
		if err != nil {
			t.Fatalf("ClaimAlbumForUpgrade after cancel: %v", err)
		}
		if !stillClaimed {
			t.Error("claim should succeed again after cancel — upgrade_pending was left set")
		}
	})
}

// TestTriggerImportRunsAndReportsResult covers the Activity page's "Import
// now" button: it should run the importer's poll immediately rather than
// waiting out its own periodic interval, and report back what it found.
// Doesn't assert imported-vs-failed for the seeded grab — internal/importer's
// own suite already covers that decision in depth — only that triggering it
// over the API actually reaches the real download store and reports a
// result, proving the route/handler/service wiring itself.
func TestTriggerImportRunsAndReportsResult(t *testing.T) {
	a := newTestAPI(t)
	sab := mockSabForRemove(t)

	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "Sabnzb", "type": "sabnzbd", "host": sab.URL, "apiKey": "key", "enabled": true,
	}, nil), http.StatusCreated)

	store := download.NewStore(a.db)
	grab := &download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "ABC123",
		Title: "Test Album", Protocol: "usenet", MediaType: "music",
	}
	if err := store.AddGrab(grab); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}

	resp := a.call("POST", "/api/v1/queue/import", nil, nil)
	a.want(resp, http.StatusAccepted)

	var state struct {
		Running bool
		Result  *struct{ Checked, Imported, Failed int }
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.call("GET", "/api/v1/queue/import/status", nil, &state)
		if !state.Running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state.Running {
		t.Fatal("import poll never finished")
	}
	if state.Result == nil || state.Result.Checked != 1 {
		t.Errorf("result = %+v, want Checked = 1", state.Result)
	}
}

// TestTriggerImportRefusesConcurrentRun mirrors the same "already running"
// guard handleTriggerMusicScan uses — a second click while one poll is still
// in flight should be turned away, not queued up behind it. The seeded
// grab's client deliberately answers slowly, so the first poll is still
// genuinely in flight (not just finished before the second request lands)
// when the second trigger arrives.
func TestTriggerImportRefusesConcurrentRun(t *testing.T) {
	a := newTestAPI(t)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte(`{"status": true}`))
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })

	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "Sabnzb", "type": "sabnzbd", "host": slow.URL, "apiKey": "key", "enabled": true,
	}, nil), http.StatusCreated)
	store := download.NewStore(a.db)
	if err := store.AddGrab(&download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "ABC123",
		Title: "Test Album", Protocol: "usenet", MediaType: "music",
	}); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}

	a.want(a.call("POST", "/api/v1/queue/import", nil, nil), http.StatusAccepted)
	// Give the background goroutine time to flip Running before the second
	// request races it — it's blocked on the slow client's queue call, not
	// close to finishing.
	time.Sleep(50 * time.Millisecond)
	a.want(a.call("POST", "/api/v1/queue/import", nil, nil), http.StatusConflict)
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

// TestRemoveQueueItemReleasesUpgradeClaim is the regression case for a gap
// in the handleGrabAlbumUpgrade claim fix itself (see ROADMAP.md item 18's
// own follow-up): handleRemoveQueueItem only ever reverted a WantedAlbumID
// grab, never an UpgradeAlbumID one — found live, removing a stuck upgrade
// download from Activity left albums.upgrade_pending set forever, since
// internal/importer's own release only fires from a grab it actually gets
// to resolve itself, not one removed out from under it. Without this, the
// album could never be upgrade-grabbed again until a full server restart.
func TestRemoveQueueItemReleasesUpgradeClaim(t *testing.T) {
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
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Test Album", "2020", "Album")
	if err != nil {
		t.Fatalf("seed album: %v", err)
	}
	claimed, err := musicStore.ClaimAlbumForUpgrade(album.ID)
	if err != nil {
		t.Fatalf("ClaimAlbumForUpgrade: %v", err)
	}
	if !claimed {
		t.Fatal("claim should succeed on a freshly-seeded album")
	}

	store := download.NewStore(a.db)
	grab := &download.GrabRecord{
		UpgradeAlbumID: album.ID, ClientConfigID: 1, ClientItemID: "XYZ789",
		Title: "Test Album", Protocol: "usenet", MediaType: "music",
	}
	if err := store.AddGrab(grab); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}

	resp := a.call("DELETE", fmt.Sprintf("/api/v1/queue/1/%s", "XYZ789"), nil, nil)
	a.want(resp, http.StatusOK)

	stillClaimed, err := musicStore.ClaimAlbumForUpgrade(album.ID)
	if err != nil {
		t.Fatalf("ClaimAlbumForUpgrade after removal: %v", err)
	}
	if !stillClaimed {
		t.Error("claim should succeed again after the stuck upgrade grab was removed — upgrade_pending was left set")
	}
}
