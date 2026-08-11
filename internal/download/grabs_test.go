package download

import "testing"

func addTestGrab(t *testing.T, s *Store, wantedAlbumID int64, status string) int64 {
	t.Helper()
	g := &GrabRecord{WantedAlbumID: wantedAlbumID, Title: "x", Protocol: "usenet", Status: status}
	if err := s.AddGrab(g); err != nil {
		t.Fatalf("AddGrab: %v", err)
	}
	return g.ID
}

// TestListGrabsForWantedAlbums is the regression test for a real bug in
// its predecessor (cancelInFlightGrabs used to fetch ListGrabs(status),
// which caps at 200 rows, then filter in Go): a scoped query must find
// every matching grab regardless of how many other in-flight grabs exist
// system-wide, and must never return a grab under the wrong status or for
// a wanted album that wasn't asked for.
func TestListGrabsForWantedAlbums(t *testing.T) {
	s := newTestService(t).Store()

	wantGrabbed := addTestGrab(t, s, 1, GrabStatusGrabbed)
	addTestGrab(t, s, 1, GrabStatusImported) // same wanted album, wrong status — excluded
	addTestGrab(t, s, 2, GrabStatusGrabbed)  // different wanted album — excluded
	wantGrabbed2 := addTestGrab(t, s, 3, GrabStatusGrabbed)

	got, err := s.ListGrabsForWantedAlbums([]int64{1, 3}, GrabStatusGrabbed)
	if err != nil {
		t.Fatalf("ListGrabsForWantedAlbums: %v", err)
	}
	gotIDs := map[int64]bool{}
	for _, g := range got {
		gotIDs[g.ID] = true
	}
	if len(got) != 2 || !gotIDs[wantGrabbed] || !gotIDs[wantGrabbed2] {
		t.Fatalf("got = %+v, want exactly the two grabbed rows for wanted albums 1 and 3", got)
	}
}

func TestListGrabsForWantedAlbumsEmptyIDs(t *testing.T) {
	s := newTestService(t).Store()
	got, err := s.ListGrabsForWantedAlbums(nil, GrabStatusGrabbed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty for no wanted album ids", got)
	}
}

// TestListGrabsForWantedAlbumsBeyondListGrabsCap confirms the scoped query
// isn't subject to ListGrabs' own LIMIT 200 — the exact gap that let an
// older in-flight grab slip past cancellation on a busy instance.
func TestListGrabsForWantedAlbumsBeyondListGrabsCap(t *testing.T) {
	s := newTestService(t).Store()

	// The target grab is added first, so it has the lowest id — and
	// ListGrabs orders newest-first (grabbed_at DESC, id DESC), so once
	// 205 more grabs land after it, it's the oldest of the lot and falls
	// outside ListGrabs(GrabStatusGrabbed)'s own 200-row cap.
	target := addTestGrab(t, s, 42, GrabStatusGrabbed)
	for i := 0; i < 205; i++ {
		addTestGrab(t, s, 1000, GrabStatusGrabbed)
	}

	capped, err := s.ListGrabs(GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range capped {
		if g.ID == target {
			found = true
		}
	}
	if found {
		t.Fatal("test setup invalid: the old grab should already be past ListGrabs' 200-row cap")
	}

	scoped, err := s.ListGrabsForWantedAlbums([]int64{42}, GrabStatusGrabbed)
	if err != nil {
		t.Fatalf("ListGrabsForWantedAlbums: %v", err)
	}
	if len(scoped) != 1 || scoped[0].ID != target {
		t.Fatalf("scoped = %+v, want exactly the one grab for wanted album 42", scoped)
	}
}

// TestGetGrab covers the lookup internal/importer's race-guard
// (stillGrabbed) depends on.
func TestGetGrab(t *testing.T) {
	s := newTestService(t).Store()
	id := addTestGrab(t, s, 7, GrabStatusGrabbed)

	got, err := s.GetGrab(id)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if got.Status != GrabStatusGrabbed || got.WantedAlbumID != 7 {
		t.Errorf("got = %+v", got)
	}

	if err := s.ResolveGrab(id, GrabStatusFailed, "canceled"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetGrab(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != GrabStatusFailed {
		t.Errorf("status after resolve = %q, want failed", got.Status)
	}
}

func TestGetGrabNotFound(t *testing.T) {
	s := newTestService(t).Store()
	if _, err := s.GetGrab(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
