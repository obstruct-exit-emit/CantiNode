package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/library"
)

type testAPI struct {
	srv    *httptest.Server
	apiKey string
	db     *sql.DB
	t      *testing.T
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	db, err := database.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	handler, _ := NewRouter(cfg, db, "test")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &testAPI{srv: srv, apiKey: cfg.APIKey, db: db, t: t}
}

// call makes an authenticated request and decodes the JSON response into out
// (skipped when out is nil or the response has no content).
func (a *testAPI) call(method, path string, body any, out any) *http.Response {
	a.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			a.t.Fatalf("encoding body: %v", err)
		}
	}
	req, err := http.NewRequest(method, a.srv.URL+path, &buf)
	if err != nil {
		a.t.Fatalf("building request: %v", err)
	}
	req.Header.Set("X-Api-Key", a.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			a.t.Fatalf("%s %s: decoding response: %v", method, path, err)
		}
	}
	return resp
}

func (a *testAPI) want(resp *http.Response, status int) {
	a.t.Helper()
	if resp.StatusCode != status {
		a.t.Fatalf("%s %s: status %d, want %d", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, status)
	}
}

func TestSearchRequiresAuth(t *testing.T) {
	a := newTestAPI(t)
	resp, err := http.Get(a.srv.URL + "/api/v1/music/artist")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without API key = %d, want 401", resp.StatusCode)
	}
}

// TestFilesystemBrowse: the folder picker lists directories (not files, not
// hidden dirs), reports the parent, and rejects unreadable paths.
func TestFilesystemBrowse(t *testing.T) {
	a := newTestAPI(t)

	base := t.TempDir()
	for _, d := range []string{"Books", "audio", ".hidden"} {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Path        string `json:"path"`
		Parent      string `json:"parent"`
		Directories []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"directories"`
	}
	a.want(a.call("GET", "/api/v1/filesystem?path="+url.QueryEscape(base), nil, &got), http.StatusOK)
	if got.Path != base || got.Parent != filepath.Dir(base) {
		t.Errorf("path/parent = %q/%q", got.Path, got.Parent)
	}
	names := []string{}
	for _, d := range got.Directories {
		names = append(names, d.Name)
	}
	if len(names) != 2 || names[0] != "audio" || names[1] != "Books" {
		t.Errorf("directories = %v, want [audio Books] (sorted, no files, no hidden)", names)
	}

	a.want(a.call("GET", "/api/v1/filesystem?path="+url.QueryEscape(filepath.Join(base, "nope")), nil, nil),
		http.StatusBadRequest)
}

// TestUserManagement: multiple login accounts — add, list, change password,
// promote to default, remove — with the default user protected.
func TestUserManagement(t *testing.T) {
	a := newTestAPI(t)

	// First account via the legacy credentials endpoint (the wizard's path).
	a.want(a.call("PUT", "/api/v1/auth/credentials",
		map[string]any{"username": "alice", "password": "password123"}, nil), http.StatusOK)

	var got struct {
		Users []struct {
			Username string `json:"username"`
			Default  bool   `json:"default"`
		} `json:"users"`
	}
	a.want(a.call("GET", "/api/v1/auth/users", nil, &got), http.StatusOK)
	if len(got.Users) != 1 || got.Users[0].Username != "alice" || !got.Users[0].Default {
		t.Fatalf("users = %+v", got.Users)
	}

	// Add a second user; validations hold.
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]any{"username": "bob", "password": "password123"}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]any{"username": "carl", "password": "short"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/auth/users",
		map[string]any{"username": "BOB", "password": "password123"}, nil), http.StatusConflict)

	// The default user is protected; promoting another frees it.
	a.want(a.call("DELETE", "/api/v1/auth/users/alice", nil, nil), http.StatusBadRequest)
	a.want(a.call("PUT", "/api/v1/auth/users/bob/default", nil, nil), http.StatusOK)
	a.want(a.call("DELETE", "/api/v1/auth/users/alice", nil, nil), http.StatusOK)

	// Change bob's password and log in with it.
	a.want(a.call("PUT", "/api/v1/auth/users/bob/password",
		map[string]any{"password": "newpassword1"}, nil), http.StatusOK)
	a.want(a.call("POST", "/api/v1/auth/login",
		map[string]any{"username": "bob", "password": "newpassword1"}, nil), http.StatusOK)

	a.want(a.call("GET", "/api/v1/auth/users", nil, &got), http.StatusOK)
	if len(got.Users) != 1 || got.Users[0].Username != "bob" || !got.Users[0].Default {
		t.Fatalf("final users = %+v", got.Users)
	}
}

// TestFirstRunSetup: a fresh instance is claimable by its first visitor with
// no API key — the setup endpoint creates the login account and signs the
// browser in; once claimed it refuses further claims.
func TestFirstRunSetup(t *testing.T) {
	a := newTestAPI(t)

	var status struct {
		Needed bool `json:"needed"`
	}
	a.want(a.call("GET", "/api/v1/setup/status", nil, &status), http.StatusOK)
	if !status.Needed {
		t.Fatal("fresh instance should need setup")
	}

	// Validation runs before the claim.
	for _, bad := range []string{
		`{"username":"","password":"password123"}`,
		`{"username":"dan","password":"short"}`,
	} {
		resp, err := http.Post(a.srv.URL+"/api/v1/auth/setup", "application/json", strings.NewReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("bad payload %s = %d, want 400", bad, resp.StatusCode)
		}
	}

	// Claim — plain unauthenticated request, no API key anywhere.
	resp, err := http.Post(a.srv.URL+"/api/v1/auth/setup", "application/json",
		strings.NewReader(`{"username":"dan","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d, want 200", resp.StatusCode)
	}
	var session string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("setup did not sign the browser in")
	}

	// The fresh session authenticates API calls without the key.
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/music/artist", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("session call = %d, want 200", authed.StatusCode)
	}

	// Claimed: the wizard is over and further claims are refused.
	a.want(a.call("GET", "/api/v1/setup/status", nil, &status), http.StatusOK)
	if status.Needed {
		t.Error("claimed instance still reports setup needed")
	}
	again, err := http.Post(a.srv.URL+"/api/v1/auth/setup", "application/json",
		strings.NewReader(`{"username":"eve","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusForbidden {
		t.Errorf("second claim = %d, want 403", again.StatusCode)
	}
}

// TestSetupRefusedOnConfiguredInstance: an instance with any configuration
// (here a root folder) but no login account is NOT claimable — the open setup
// endpoint must not let a visitor hijack a key-authenticated install.
func TestSetupRefusedOnConfiguredInstance(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": t.TempDir()}, nil), http.StatusCreated)

	var status struct {
		Needed bool `json:"needed"`
	}
	a.want(a.call("GET", "/api/v1/setup/status", nil, &status), http.StatusOK)
	if status.Needed {
		t.Error("configured instance should not need setup")
	}
	resp, err := http.Post(a.srv.URL+"/api/v1/auth/setup", "application/json",
		strings.NewReader(`{"username":"eve","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("claim on configured instance = %d, want 403", resp.StatusCode)
	}
}
func TestDownloadClientsCRUD(t *testing.T) {
	a := newTestAPI(t)

	// Minimal SABnzbd mock.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "version":
			w.Write([]byte(`{"version": "4.3.2"}`))
		case "addurl":
			w.Write([]byte(`{"status": true, "nzo_ids": ["nzo_1"]}`))
		case "queue":
			// SABnzbd names the queue slot's category field "cat" (history uses
			// "category" instead — see sabSlot in internal/download/sabnzbd.go).
			w.Write([]byte(`{"queue": {"slots": [{"nzo_id": "nzo_1", "filename": "Mort", "status": "Downloading", "percentage": "50", "cat": "cantinode"}]}}`))
		case "history":
			w.Write([]byte(`{"history": {"slots": []}}`))
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
	defer srv.Close()

	// Validation: unknown type is rejected; a bad host too.
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "x", "type": "transmission", "host": srv.URL}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "x", "type": "sabnzbd", "host": "not-a-url"}, nil), http.StatusBadRequest)

	// SABnzbd needs no API key — a keyless client (Real-Debrid's fake-SAB
	// endpoint) tests and saves fine.
	a.want(a.call("POST", "/api/v1/downloadclient/test",
		map[string]any{"name": "rd", "type": "sabnzbd", "host": srv.URL}, nil), http.StatusOK)
	var keyless download.ClientConfig
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "rd", "type": "sabnzbd", "host": srv.URL}, &keyless), http.StatusCreated)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/downloadclient/%d", keyless.ID), nil, nil), http.StatusNoContent)

	// Test-before-save, then create (with a key this time).
	a.want(a.call("POST", "/api/v1/downloadclient/test",
		map[string]any{"name": "sab", "type": "sabnzbd", "host": srv.URL, "apiKey": "k"}, nil), http.StatusOK)
	var client download.ClientConfig
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "sab", "type": "sabnzbd", "host": srv.URL, "apiKey": "k", "enabled": true}, &client), http.StatusCreated)
	if client.Category != "cantinode" || client.Priority != 1 {
		t.Fatalf("client defaults = %+v", client)
	}

	// Queue reads straight from the live client (the mock always reports one
	// slot) — grab/protocol-routing mechanics are covered directly in
	// internal/download's own tests (TestServiceGrabAndQueue); music's grab
	// path (POST /api/v1/music/wanted/{id}/grab) exercises the same
	// download.Service.GrabRelease at the HTTP layer.
	var queue struct {
		Items []download.Item `json:"items"`
	}
	a.want(a.call("GET", "/api/v1/queue", nil, &queue), http.StatusOK)
	if len(queue.Items) != 1 || queue.Items[0].Status != "downloading" || queue.Items[0].Progress != 0.5 {
		t.Fatalf("queue = %+v", queue.Items)
	}

	client.Enabled = false
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/downloadclient/%d", client.ID), client, nil), http.StatusOK)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/downloadclient/%d", client.ID), nil, nil), http.StatusNoContent)
}

func TestQualityProfiles(t *testing.T) {
	a := newTestAPI(t)

	// Seeded default present.
	var profiles []library.QualityProfile
	a.want(a.call("GET", "/api/v1/qualityprofile", nil, &profiles), http.StatusOK)
	if len(profiles) != 1 || profiles[0].Name != "Standard Music" || !profiles[0].IsDefault {
		t.Fatalf("profiles = %+v", profiles)
	}
	seeded := profiles[0].ID

	// Create a flac-only profile; validation rejects junk.
	a.want(a.call("POST", "/api/v1/qualityprofile",
		map[string]any{"name": "Bad", "formats": []string{"docx"}}, nil), http.StatusBadRequest)
	var flacOnly library.QualityProfile
	a.want(a.call("POST", "/api/v1/qualityprofile",
		map[string]any{"name": "FLAC Only", "formats": []string{"flac"}, "language": "english"}, &flacOnly), http.StatusCreated)
	if flacOnly.IsDefault {
		t.Error("new profile must not steal default")
	}

	// Default swap and guarded delete.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/qualityprofile/%d/default", flacOnly.ID), nil, nil), http.StatusOK)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/qualityprofile/%d", flacOnly.ID), nil, nil), http.StatusBadRequest)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/qualityprofile/%d", seeded), nil, nil), http.StatusNoContent)

	// Update the remaining profile.
	var updated library.QualityProfile
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/qualityprofile/%d", flacOnly.ID),
		map[string]any{"name": "FLAC Only", "formats": []string{"flac", "mp3"}, "retailBonus": 30}, &updated), http.StatusOK)
	if len(updated.Formats) != 2 || updated.RetailBonus != 30 || !updated.IsDefault {
		t.Errorf("updated = %+v", updated)
	}
}
