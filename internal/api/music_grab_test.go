package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/download"
)

// mockSabForGrab is a minimal SABnzbd double: just enough for
// download.Service.GrabRelease's usenet path (fetch the NZB, upload via
// addfile) to succeed. Not the full mockSab in internal/download's own
// tests — this package doesn't need the queue/history endpoints, only Add.
func mockSabForGrab(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/get/") {
			w.Header().Set("Content-Type", "application/x-nzb")
			w.Write([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"></file></nzb>`))
			return
		}
		switch r.URL.Query().Get("mode") {
		case "version":
			w.Write([]byte(`{"version": "4.3.2"}`))
		case "addfile":
			w.Write([]byte(`{"status": true, "nzo_ids": ["nzo_1"]}`))
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGrabWantedMusicAlbumClaimsBeforeGrabbing is the regression test for a
// real race: grabbing used to set wanted_albums.status to "downloading"
// only *after* a successful grab, so two callers reading "still wanted" at
// the same moment — most realistically the automatic wanted-list sweep
// (internal/autosearch) landing on the same album a user is manually
// grabbing — could both grab it, producing duplicate downloads. The
// second grab attempt against the same wanted album must now be refused
// outright (409) rather than silently succeeding a second time.
func TestGrabWantedMusicAlbumClaimsBeforeGrabbing(t *testing.T) {
	a := newTestAPI(t)
	mock := mockSabForGrab(t)

	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "sab", "type": "sabnzbd", "host": mock.URL, "enabled": true,
	}, nil), http.StatusCreated)

	wantedID := seedWantedAlbum(t, a)

	grabBody := map[string]any{
		"title":       "Boards of Canada - Geogaddi",
		"downloadUrl": mock.URL + "/get/1",
		"guid":        "guid-1",
		"protocol":    download.ProtocolUsenet,
	}
	var first struct {
		Client string `json:"client"`
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/music/wanted/%d/grab", wantedID), grabBody, &first), http.StatusOK)
	if first.Client == "" {
		t.Fatalf("first grab: expected a client name in the response")
	}

	// Same wanted album, already "downloading" from the first grab — must
	// be refused, not grabbed again.
	a.want(a.call("POST", fmt.Sprintf("/api/v1/music/wanted/%d/grab", wantedID), grabBody, nil), http.StatusConflict)
}
