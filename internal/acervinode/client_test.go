package acervinode

import (
	"testing"
)

func newTestClient(t *testing.T, apiKey string) (*Client, *fakeServer) {
	t.Helper()
	f := newFakeServer(t, apiKey)
	srv := f.start()
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, apiKey), f
}

func TestAddMagnetAndGetTorrentStatus(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	magnet := "magnet:?xt=urn:btih:ABCDEF1234567890abcdef1234567890ABCDEF12&dn=Test"

	hash, err := c.AddMagnet(t.Context(), magnet)
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if hash != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Errorf("hash = %q", hash)
	}
	if f.loginCount != 1 {
		t.Errorf("loginCount = %d, want 1", f.loginCount)
	}

	status, err := c.GetTorrentStatus(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetTorrentStatus: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("State = %q, want downloading", status.State)
	}

	// Simulate AcerviNode reporting completion.
	f.torrents[hash].state = "pausedUP"
	f.torrents[hash].contentPath = "/downloads/music/Test Release"
	status, err = c.GetTorrentStatus(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateCompleted || status.LocalPath != "/downloads/music/Test Release" {
		t.Errorf("status = %+v", status)
	}
}

func TestGetTorrentStatusErrorState(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	hash, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:0000000000000000000000000000000000000a")
	if err != nil {
		t.Fatal(err)
	}
	f.torrents[hash].state = "error"

	status, err := c.GetTorrentStatus(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateError {
		t.Errorf("State = %q, want error", status.State)
	}
}

func TestGetTorrentStatusNotFound(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	if _, err := c.GetTorrentStatus(t.Context(), "0000000000000000000000000000000000000a"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddTorrentFileFindsNewHashByDiff(t *testing.T) {
	c, _ := newTestClient(t, "test-key")

	// Seed one existing torrent in the category so the diff logic is
	// actually exercised, not just "any hash present."
	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:1111111111111111111111111111111111111a"); err != nil {
		t.Fatal(err)
	}

	hash, err := c.AddTorrentFile(t.Context(), "release.torrent", []byte("fake bencoded torrent data"))
	if err != nil {
		t.Fatalf("AddTorrentFile: %v", err)
	}
	if hash == "" || hash == "1111111111111111111111111111111111111a" {
		t.Errorf("hash = %q, want a new, different hash", hash)
	}
}

func TestSessionExpiryTriggersRelogin(t *testing.T) {
	c, f := newTestClient(t, "test-key")

	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:2222222222222222222222222222222222222a"); err != nil {
		t.Fatal(err)
	}
	if f.loginCount != 1 {
		t.Fatalf("loginCount = %d, want 1", f.loginCount)
	}

	// Simulate AcerviNode's session expiring server-side without the
	// client knowing.
	for sid := range f.validSIDs {
		delete(f.validSIDs, sid)
	}

	status, err := c.GetTorrentStatus(t.Context(), "2222222222222222222222222222222222222a")
	if err != nil {
		t.Fatalf("GetTorrentStatus after session expiry: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("status = %+v", status)
	}
	if f.loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (should have re-logged in once)", f.loginCount)
	}
}

func TestAddMagnetWrongAPIKeyFailsLogin(t *testing.T) {
	f := newFakeServer(t, "real-key")
	srv := f.start()
	defer srv.Close()
	c := NewClient(srv.URL, "wrong-key")

	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:3333333333333333333333333333333333333a"); err == nil {
		t.Error("expected an error with a wrong api key")
	}
}

func TestAddNZBByURLAndGetUsenetStatus(t *testing.T) {
	c, f := newTestClient(t, "test-key")

	nzoID, err := c.AddNZBByURL(t.Context(), "http://indexer.example/release.nzb", "Test Release")
	if err != nil {
		t.Fatalf("AddNZBByURL: %v", err)
	}
	if nzoID == "" {
		t.Fatal("nzoID is empty")
	}

	status, err := c.GetUsenetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatalf("GetUsenetStatus: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("State = %q, want downloading", status.State)
	}

	// Simulate completion: move from queue to history.
	f.nzbs[nzoID].inQueue = false
	f.nzbs[nzoID].historyStat = "Completed"
	f.nzbs[nzoID].storage = "/downloads/music/Test Release"

	status, err = c.GetUsenetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateCompleted || status.LocalPath != "/downloads/music/Test Release" {
		t.Errorf("status = %+v", status)
	}
}

func TestGetUsenetStatusFailed(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	nzoID, err := c.AddNZBByURL(t.Context(), "http://indexer.example/release.nzb", "Test")
	if err != nil {
		t.Fatal(err)
	}
	f.nzbs[nzoID].inQueue = false
	f.nzbs[nzoID].historyStat = "Failed"
	f.nzbs[nzoID].failMessage = "par2 repair failed"

	status, err := c.GetUsenetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateError || status.ErrorMessage != "par2 repair failed" {
		t.Errorf("status = %+v", status)
	}
}

func TestGetUsenetStatusNotFound(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	if _, err := c.GetUsenetStatus(t.Context(), "nzo-nonexistent"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddNZBByFile(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	nzoID, err := c.AddNZBByFile(t.Context(), "release.nzb", []byte("fake nzb xml"), "Test Release")
	if err != nil {
		t.Fatalf("AddNZBByFile: %v", err)
	}
	if nzoID == "" {
		t.Error("nzoID is empty")
	}
}

func TestAddNZBWrongAPIKeyFails(t *testing.T) {
	f := newFakeServer(t, "real-key")
	srv := f.start()
	defer srv.Close()
	c := NewClient(srv.URL, "wrong-key")

	if _, err := c.AddNZBByURL(t.Context(), "http://indexer.example/x.nzb", "X"); err == nil {
		t.Error("expected an error with a wrong api key")
	}
}
