package api

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/cantinode/cantinode/internal/download"
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
