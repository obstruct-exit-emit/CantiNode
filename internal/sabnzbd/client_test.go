package sabnzbd

import "testing"

func newTestClient(t *testing.T, apiKey string) (*Client, *fakeServer) {
	t.Helper()
	f := newFakeServer(apiKey)
	srv := f.start()
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, apiKey), f
}

func TestAddByURLAndGetStatus(t *testing.T) {
	c, f := newTestClient(t, "test-key")

	nzoID, err := c.AddByURL(t.Context(), "http://indexer.example/release.nzb", "Test Release")
	if err != nil {
		t.Fatalf("AddByURL: %v", err)
	}
	if nzoID == "" {
		t.Fatal("nzoID is empty")
	}

	status, err := c.GetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.State != StateDownloading {
		t.Errorf("State = %q, want downloading", status.State)
	}

	// Simulate completion: move from queue to history.
	f.nzbs[nzoID].inQueue = false
	f.nzbs[nzoID].historyStat = "Completed"
	f.nzbs[nzoID].storage = "/downloads/music/Test Release"

	status, err = c.GetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateCompleted || status.LocalPath != "/downloads/music/Test Release" {
		t.Errorf("status = %+v", status)
	}
}

func TestRemoveFromQueue(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	nzoID, err := c.AddByURL(t.Context(), "http://indexer.example/release.nzb", "Test")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Remove(t.Context(), nzoID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := f.nzbs[nzoID]; ok {
		t.Error("nzb should be gone from the fake server after Remove")
	}
	if _, err := c.GetStatus(t.Context(), nzoID); err != ErrNotFound {
		t.Errorf("GetStatus after Remove: err = %v, want ErrNotFound", err)
	}
}

func TestRemoveFromHistory(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	nzoID, err := c.AddByURL(t.Context(), "http://indexer.example/release.nzb", "Test")
	if err != nil {
		t.Fatal(err)
	}
	f.nzbs[nzoID].inQueue = false
	f.nzbs[nzoID].historyStat = "Completed"

	if err := c.Remove(t.Context(), nzoID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := f.nzbs[nzoID]; ok {
		t.Error("nzb should be gone from the fake server after Remove")
	}
}

func TestRemoveUnknownIDIsNotAnError(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	if err := c.Remove(t.Context(), "nzo-nonexistent"); err != nil {
		t.Errorf("Remove of an unknown id should not error: %v", err)
	}
}

func TestGetStatusFailed(t *testing.T) {
	c, f := newTestClient(t, "test-key")
	nzoID, err := c.AddByURL(t.Context(), "http://indexer.example/release.nzb", "Test")
	if err != nil {
		t.Fatal(err)
	}
	f.nzbs[nzoID].inQueue = false
	f.nzbs[nzoID].historyStat = "Failed"
	f.nzbs[nzoID].failMessage = "par2 repair failed"

	status, err := c.GetStatus(t.Context(), nzoID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StateError || status.ErrorMessage != "par2 repair failed" {
		t.Errorf("status = %+v", status)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	if _, err := c.GetStatus(t.Context(), "nzo-nonexistent"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddByFile(t *testing.T) {
	c, _ := newTestClient(t, "test-key")
	nzoID, err := c.AddByFile(t.Context(), "release.nzb", []byte("fake nzb xml"), "Test Release")
	if err != nil {
		t.Fatalf("AddByFile: %v", err)
	}
	if nzoID == "" {
		t.Error("nzoID is empty")
	}
}

func TestAddByURLWrongAPIKeyFails(t *testing.T) {
	f := newFakeServer("real-key")
	srv := f.start()
	defer srv.Close()
	c := NewClient(srv.URL, "wrong-key")

	if _, err := c.AddByURL(t.Context(), "http://indexer.example/x.nzb", "X"); err == nil {
		t.Error("expected an error with a wrong api key")
	}
}
