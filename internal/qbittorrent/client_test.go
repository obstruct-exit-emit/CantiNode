package qbittorrent

import "testing"

func newTestClient(t *testing.T, username, password string) (*Client, *fakeServer) {
	t.Helper()
	f := newFakeServer(t, username, password)
	srv := f.start()
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, username, password), f
}

func TestAddMagnetAndGetStatus(t *testing.T) {
	c, f := newTestClient(t, "testuser", "test-key")
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

	status, err := c.GetStatus(t.Context(), hash)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("State = %q, want downloading", status.State)
	}

	// Simulate the server reporting completion.
	f.torrents[hash].state = "pausedUP"
	f.torrents[hash].contentPath = "/downloads/music/Test Release"
	status, err = c.GetStatus(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateCompleted || status.LocalPath != "/downloads/music/Test Release" {
		t.Errorf("status = %+v", status)
	}
}

func TestRemoveDeletesTorrentAndFiles(t *testing.T) {
	c, _ := newTestClient(t, "testuser", "test-key")
	hash, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:5555555555555555555555555555555555555a")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Remove(t.Context(), hash); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := c.GetStatus(t.Context(), hash); err != ErrNotFound {
		t.Errorf("GetStatus after Remove: err = %v, want ErrNotFound", err)
	}
}

func TestRemoveUnknownHashIsNotAnError(t *testing.T) {
	c, _ := newTestClient(t, "testuser", "test-key")
	if err := c.Remove(t.Context(), "0000000000000000000000000000000000000a"); err != nil {
		t.Errorf("Remove of an unknown hash should not error: %v", err)
	}
}

func TestGetStatusErrorState(t *testing.T) {
	c, f := newTestClient(t, "testuser", "test-key")
	hash, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:0000000000000000000000000000000000000a")
	if err != nil {
		t.Fatal(err)
	}
	f.torrents[hash].state = "error"

	status, err := c.GetStatus(t.Context(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateError {
		t.Errorf("State = %q, want error", status.State)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	c, _ := newTestClient(t, "testuser", "test-key")
	if _, err := c.GetStatus(t.Context(), "0000000000000000000000000000000000000a"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddTorrentFileFindsNewHashByDiff(t *testing.T) {
	c, _ := newTestClient(t, "testuser", "test-key")

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
	c, f := newTestClient(t, "testuser", "test-key")

	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:2222222222222222222222222222222222222a"); err != nil {
		t.Fatal(err)
	}
	if f.loginCount != 1 {
		t.Fatalf("loginCount = %d, want 1", f.loginCount)
	}

	// Simulate the server's session expiring without the client knowing.
	for sid := range f.validSIDs {
		delete(f.validSIDs, sid)
	}

	status, err := c.GetStatus(t.Context(), "2222222222222222222222222222222222222a")
	if err != nil {
		t.Fatalf("GetStatus after session expiry: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("status = %+v", status)
	}
	if f.loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (should have re-logged in once)", f.loginCount)
	}
}

func TestAddMagnetWrongCredentialsFailsLogin(t *testing.T) {
	f := newFakeServer(t, "realuser", "real-key")
	srv := f.start()
	defer srv.Close()
	c := NewClient(srv.URL, "realuser", "wrong-key")

	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:3333333333333333333333333333333333333a"); err == nil {
		t.Error("expected an error with a wrong password")
	}
}

func TestAddMagnetWrongUsernameFailsLogin(t *testing.T) {
	f := newFakeServer(t, "realuser", "real-key")
	srv := f.start()
	defer srv.Close()
	c := NewClient(srv.URL, "wronguser", "real-key")

	if _, err := c.AddMagnet(t.Context(), "magnet:?xt=urn:btih:4444444444444444444444444444444444444a"); err == nil {
		t.Error("expected an error with a wrong username")
	}
}
