package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestGrabWantedMusicAlbumSurvivesClientDisconnect is the regression test
// for the same class of bug metadataCtx's own fix addressed (see
// shared.go's doc comment on that pattern, and downloads.go's on
// downloadCtx): a grab's context used to derive from r.Context(), so a
// client disconnect mid-request (a page refresh, navigating away) canceled
// the in-flight download-client submission — exactly the "abandon adds
// that then land unrecorded" outcome downloadTimeout's own doc comment
// exists to avoid, just reached by a different route than a too-tight
// timeout. The mock download client here deliberately answers slower than
// the test's own client is willing to wait, simulating a real disconnect
// the same way this project's own live verification used a forced-timeout
// curl against a real server for the analogous match-approval fix. The
// grab must still land server-side (wanted_albums.status stays
// "downloading", not reverted back to "wanted") even though the client
// that asked for it gave up first.
func TestGrabWantedMusicAlbumSurvivesClientDisconnect(t *testing.T) {
	a := newTestAPI(t)

	// Comfortably longer than the test client's own timeout below, so the
	// client gives up well before this responds — simulating a debrid
	// bridge or a slow download client, the exact case downloadTimeout's
	// own doc comment describes.
	const respondDelay = 150 * time.Millisecond
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/get/") {
			w.Header().Set("Content-Type", "application/x-nzb")
			w.Write([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="x"></file></nzb>`))
			return
		}
		switch r.URL.Query().Get("mode") {
		case "version":
			w.Write([]byte(`{"version": "4.3.2"}`))
		case "addfile":
			time.Sleep(respondDelay)
			w.Write([]byte(`{"status": true, "nzo_ids": ["nzo_1"]}`))
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
	t.Cleanup(mock.Close)

	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "sab", "type": "sabnzbd", "host": mock.URL, "enabled": true,
	}, nil), http.StatusCreated)

	wantedID := seedWantedAlbum(t, a)

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]any{
		"title":       "Boards of Canada - Geogaddi",
		"downloadUrl": mock.URL + "/get/1",
		"guid":        "guid-1",
		"protocol":    download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/music/wanted/%d/grab", a.srv.URL, wantedID), &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)
	req.Header.Set("Content-Type", "application/json")

	// Far shorter than respondDelay — this client gives up (and closes its
	// connection) before the mock download client responds, propagating a
	// canceled context to the server's own r.Context() the same way a real
	// browser refresh/navigation does.
	client := &http.Client{Timeout: 20 * time.Millisecond}
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected the client request to time out before the mock download client responds")
	}

	// Give the server-side grab time to actually finish — the whole point
	// being that it does, even though the client above already gave up.
	time.Sleep(respondDelay + 300*time.Millisecond)

	var status string
	if err := a.db.QueryRow(`SELECT status FROM wanted_albums WHERE id = ?`, wantedID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "downloading" {
		t.Errorf("wanted_albums.status = %q, want %q — the grab must complete server-side despite the client disconnecting first", status, "downloading")
	}
}
