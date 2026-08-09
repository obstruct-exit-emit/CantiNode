package api

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

func init() {
	// Register the fake provider so settings endpoints can build it.
	metadata.Register("fake", func(s metadata.Settings) (metadata.Provider, error) {
		if s.Token == "" {
			return nil, metadata.ErrNotConfigured
		}
		return fakeProvider{}, nil
	})
}

// fakeProvider is an in-memory metadata.Provider with a tiny Discworld corpus.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) SearchAuthors(_ context.Context, query string) ([]metadata.Author, error) {
	return []metadata.Author{{ForeignID: "100", Name: "Terry Pratchett", BookCount: 2}}, nil
}

func (fakeProvider) SearchBooks(_ context.Context, query string) ([]metadata.Book, error) {
	return []metadata.Book{{ForeignID: "1", Title: "The Colour of Magic", AuthorName: "Terry Pratchett"}}, nil
}

func (p fakeProvider) GetAuthor(_ context.Context, foreignID string) (*metadata.Author, error) {
	if foreignID != "100" {
		return nil, metadata.ErrNotFound
	}
	tcom, _ := p.GetBook(context.Background(), "1")
	tcom.Editions = nil // author lookups don't include editions
	return &metadata.Author{
		ForeignID:   "100",
		Name:        "Terry Pratchett",
		Description: "Sir Terry.",
		Books: []metadata.Book{
			*tcom,
			{ForeignID: "2", Title: "Mort", AuthorForeignID: "100", AuthorName: "Terry Pratchett"},
		},
	}, nil
}

func (fakeProvider) GetBook(_ context.Context, foreignID string) (*metadata.Book, error) {
	if foreignID != "1" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.Book{
		ForeignID:       "1",
		Title:           "The Colour of Magic",
		ReleaseDate:     "1983-11-24",
		Rating:          4.1,
		AuthorForeignID: "100",
		AuthorName:      "Terry Pratchett",
		Series:          []metadata.SeriesLink{{ForeignID: "7", Title: "Discworld", Position: 1}},
		Editions: []metadata.Edition{
			{ForeignID: "11", ISBN13: "9780061020711", Format: "ebook"},
			{ForeignID: "12", ASIN: "B000W94ATC", Format: "audiobook"},
			{ForeignID: "13", Format: "physical"},
		},
	}, nil
}

type testAPI struct {
	srv    *httptest.Server
	apiKey string
	mgr    *metadata.Manager
	db     *sql.DB
	t      *testing.T
}

func newTestAPI(t *testing.T, provider metadata.Provider) *testAPI {
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

	mgr := metadata.NewManager()
	if provider != nil {
		mgr.Set(provider)
	}
	handler, _ := NewRouter(cfg, db, mgr, "test")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &testAPI{srv: srv, apiKey: cfg.APIKey, mgr: mgr, db: db, t: t}
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

// callUA is call with a custom User-Agent — Prowlarr identifies itself in the
// UA, so the capability endpoints serve Readarr-shaped resources to it.
func (a *testAPI) callUA(userAgent, method, path string, body, out any) *http.Response {
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
	req.Header.Set("User-Agent", userAgent)
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
	a := newTestAPI(t, fakeProvider{})
	resp, err := http.Get(a.srv.URL + "/api/v1/search?term=x")
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
	a := newTestAPI(t, fakeProvider{})

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
	a := newTestAPI(t, fakeProvider{})

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
	a := newTestAPI(t, fakeProvider{})

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
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/book", nil)
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
	a := newTestAPI(t, fakeProvider{})
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": t.TempDir()}, nil), http.StatusCreated)

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

func TestSearch(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var books []metadata.Book
	a.want(a.call("GET", "/api/v1/search?term=magic", nil, &books), http.StatusOK)
	if len(books) != 1 || books[0].Title != "The Colour of Magic" {
		t.Errorf("book search results = %+v", books)
	}

	var authors []metadata.Author
	a.want(a.call("GET", "/api/v1/search?term=pratchett&type=author", nil, &authors), http.StatusOK)
	if len(authors) != 1 || authors[0].ForeignID != "100" {
		t.Errorf("author search results = %+v", authors)
	}

	a.want(a.call("GET", "/api/v1/search?type=book", nil, nil), http.StatusBadRequest)
	a.want(a.call("GET", "/api/v1/search?term=x&type=magazine", nil, nil), http.StatusBadRequest)
}

func TestSearchWithoutProvider(t *testing.T) {
	a := newTestAPI(t, nil)
	a.want(a.call("GET", "/api/v1/search?term=x", nil, nil), http.StatusServiceUnavailable)
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, nil), http.StatusServiceUnavailable)
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, nil), http.StatusServiceUnavailable)
	// Refresh resolves the record first (its provider override may apply),
	// so a missing record 404s before the provider check.
	a.want(a.call("POST", "/api/v1/author/1/refresh", nil, nil), http.StatusNotFound)
	a.want(a.call("POST", "/api/v1/book/1/refresh", nil, nil), http.StatusNotFound)
}

func TestMetadataSettingsHotSwap(t *testing.T) {
	a := newTestAPI(t, nil)

	// No provider yet: search unavailable, settings show what's registerable.
	a.want(a.call("GET", "/api/v1/search?term=x", nil, nil), http.StatusServiceUnavailable)
	var settings struct {
		Active    string                       `json:"active"`
		Available []string                     `json:"available"`
		Providers map[string]metadata.Settings `json:"providers"`
	}
	a.want(a.call("GET", "/api/v1/settings/metadata", nil, &settings), http.StatusOK)
	found := false
	for _, name := range settings.Available {
		if name == "fake" {
			found = true
		}
	}
	if !found {
		t.Fatalf("available providers %v missing 'fake'", settings.Available)
	}

	// Unknown provider name is rejected.
	a.want(a.call("PUT", "/api/v1/settings/metadata",
		map[string]any{"active": "bogus"}, nil), http.StatusBadRequest)

	// Test button: empty token rejected, real token accepted.
	a.want(a.call("POST", "/api/v1/settings/metadata/test",
		map[string]any{"provider": "fake", "settings": map[string]string{"token": ""}}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/settings/metadata/test",
		map[string]any{"provider": "fake", "settings": map[string]string{"token": "tok"}}, nil), http.StatusOK)

	// Saving a token activates the provider without a restart.
	a.want(a.call("PUT", "/api/v1/settings/metadata", map[string]any{
		"active":    "fake",
		"providers": map[string]any{"fake": map[string]string{"token": "tok"}},
	}, &settings), http.StatusOK)
	if settings.Active != "fake" || settings.Providers["fake"].Token != "tok" {
		t.Errorf("settings after save = %+v", settings)
	}
	a.want(a.call("GET", "/api/v1/search?term=magic", nil, nil), http.StatusOK)

	// Clearing the token deactivates it again.
	a.want(a.call("PUT", "/api/v1/settings/metadata", map[string]any{
		"active":    "fake",
		"providers": map[string]any{"fake": map[string]string{"token": ""}},
	}, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/search?term=magic", nil, nil), http.StatusServiceUnavailable)
}

// TestSeriesProviderNone: "none" disables a media type's provider — manga
// search goes unavailable AND refreshes fetch nothing (libraries always
// honor the settings). The per-series provider override is the explicit
// escape hatch: once set, refresh uses that provider even under "none".
func TestSeriesProviderNone(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 2
	// Registered (not just injected) so the settings PUT's ConfigureSeries
	// rebuild keeps the provider available by name.
	metadata.RegisterSeries("fakemanga", func(metadata.Settings) (metadata.SeriesProvider, error) {
		return fakeSeriesProvider{volumes: &volumes}, nil
	})
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500"}, &series), http.StatusCreated)

	// Disable the manga provider via settings ("none" must validate).
	var settings struct {
		MangaProvider string `json:"mangaProvider"`
	}
	a.want(a.call("PUT", "/api/v1/settings/metadata", map[string]any{
		"active": "", "providers": map[string]any{}, "mangaProvider": "none",
	}, &settings), http.StatusOK)
	if settings.MangaProvider != "none" {
		t.Fatalf("mangaProvider = %q, want none", settings.MangaProvider)
	}
	a.want(a.call("GET", "/api/v1/search?term=berserk&type=manga", nil, nil), http.StatusServiceUnavailable)

	// A refresh under provider=none fetches NOTHING — the volume count must
	// not change even though the provider now reports more volumes.
	volumes = 3
	var refreshed library.Series
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/refresh", series.ID), nil, &refreshed), http.StatusOK)
	if len(refreshed.Volumes) != 2 {
		t.Fatalf("refresh under provider=none fetched metadata: %d volumes, want 2", len(refreshed.Volumes))
	}

	// The per-series provider override beats the global "none".
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/series/%d/provider", series.ID),
		map[string]string{"provider": "fakemanga"}, &refreshed), http.StatusOK)
	if refreshed.ProviderOverride != "fakemanga" {
		t.Fatalf("providerOverride = %q, want fakemanga", refreshed.ProviderOverride)
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/refresh", series.ID), nil, &refreshed), http.StatusOK)
	if len(refreshed.Volumes) != 3 {
		t.Fatalf("refresh with override got %d volumes, want 3", len(refreshed.Volumes))
	}

	// Clearing the override re-honors the global setting.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/series/%d/provider", series.ID),
		map[string]string{"provider": ""}, &refreshed), http.StatusOK)
	volumes = 4
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/refresh", series.ID), nil, &refreshed), http.StatusOK)
	if len(refreshed.Volumes) != 3 {
		t.Fatalf("refresh after clearing override fetched metadata under none: %d volumes, want 3", len(refreshed.Volumes))
	}
}

// fakeSeriesProvider serves one manga series whose volume count can grow.
type fakeSeriesProvider struct {
	volumes *int
}

func (fakeSeriesProvider) Name() string      { return "fakemanga" }
func (fakeSeriesProvider) MediaType() string { return "manga" }

func (p fakeSeriesProvider) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return []metadata.SeriesResult{{ForeignID: "500", Title: "Berserk", AuthorName: "Kentarou Miura", IssueCount: *p.volumes}}, nil
}

func (p fakeSeriesProvider) GetSeries(_ context.Context, id string) (*metadata.SeriesResult, error) {
	if id != "500" {
		return nil, metadata.ErrNotFound
	}
	result := &metadata.SeriesResult{
		ForeignID: "500", Title: "Berserk", AuthorName: "Kentarou Miura",
		Description: "Guts.", CoverURL: "https://img/berserk.jpg", IssueCount: *p.volumes,
	}
	for i := 1; i <= *p.volumes; i++ {
		result.Issues = append(result.Issues, metadata.Issue{
			ForeignID: fmt.Sprintf("500-v%d", i), Number: float64(i), Title: fmt.Sprintf("Vol. %d", i),
		})
	}
	return result, nil
}

// fakeComicProvider is fakeSeriesProvider serving the comic media type.
type fakeComicProvider struct{ fakeSeriesProvider }

func (fakeComicProvider) Name() string      { return "fakecomic" }
func (fakeComicProvider) MediaType() string { return "comic" }

func TestSeriesFlow(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 3
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	// Search via the shared search endpoint.
	var results []metadata.SeriesResult
	a.want(a.call("GET", "/api/v1/search?term=berserk&type=manga", nil, &results), http.StatusOK)
	if len(results) != 1 || results[0].ForeignID != "500" {
		t.Fatalf("search = %+v", results)
	}
	// Comic search has no provider configured.
	a.want(a.call("GET", "/api/v1/search?term=x&type=comic", nil, nil), http.StatusServiceUnavailable)

	// Add the series with explicit monitoring (the default pulls metadata
	// only): volumes become monitored manga books.
	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500", "monitored": true, "monitorNew": true}, &series), http.StatusCreated)
	if series.Title != "Berserk" || !series.Monitored || !series.MonitorNew || series.MediaType != "manga" {
		t.Fatalf("series = %+v", series)
	}
	if len(series.Volumes) != 3 {
		t.Fatalf("volumes = %+v", series.Volumes)
	}
	v1 := series.Volumes[0]
	if v1.Title != "Berserk Vol. 1" || v1.MediaType != "manga" || !v1.Monitored {
		t.Fatalf("volume 1 = %+v", v1)
	}

	// List filters by media type.
	var list []library.Series
	a.want(a.call("GET", "/api/v1/series?mediaType=manga", nil, &list), http.StatusOK)
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	a.want(a.call("GET", "/api/v1/series?mediaType=comic", nil, &list), http.StatusOK)
	if len(list) != 0 {
		t.Fatalf("comic list = %+v", list)
	}

	// Refresh discovers a new volume; monitor_new makes it monitored.
	volumes = 4
	var refreshed library.Series
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/refresh", series.ID), nil, &refreshed), http.StatusOK)
	if len(refreshed.Volumes) != 4 || !refreshed.Volumes[3].Monitored {
		t.Fatalf("after refresh = %+v", refreshed.Volumes)
	}

	// Unmonitor the series: volumes follow.
	var updated library.Series
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/series/%d/monitor", series.ID),
		map[string]bool{"monitored": false, "monitorNew": false}, &updated), http.StatusOK)
	for _, v := range updated.Volumes {
		if v.Monitored {
			t.Fatalf("volume still monitored: %+v", v)
		}
	}

	// Delete removes the series and its volumes.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/series/%d", series.ID), nil, nil), http.StatusNoContent)
	var books []library.Book
	a.want(a.call("GET", "/api/v1/book", nil, &books), http.StatusOK)
	if len(books) != 0 {
		t.Fatalf("volumes survived series delete: %+v", books)
	}

	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "999"}, nil), http.StatusNotFound)
}

// TestMangaVolumeLibrary: a manga volume is in the library when monitored or
// owned; PUT /book/{id}/library with library=manga removes it (unmonitor,
// moving it to the series' Missing section) or adds it back (monitor). A
// volume's own monitor toggle works the same way.
func TestMangaVolumeLibrary(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 3
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500"}, &series), http.StatusCreated)
	if len(series.Volumes) != 3 {
		t.Fatalf("volumes = %d, want 3", len(series.Volumes))
	}
	v1 := series.Volumes[0].ID

	// Like adding an author, adding a series pulls metadata only: every
	// volume starts unmonitored — the whole series begins in Missing.
	if series.Volumes[0].Monitored {
		t.Fatal("freshly added volume must NOT be monitored (metadata only)")
	}

	// Monitor volume 1 from Missing (member=true adds it to the library).
	var book library.Book
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "manga", "member": true}, &book), http.StatusOK)
	if !book.Monitored {
		t.Fatalf("monitored volume from Missing = %+v", book)
	}

	// Remove it again: it unmonitors (unowned → back to Missing).
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "manga", "member": false}, &book), http.StatusOK)
	if book.Monitored {
		t.Fatalf("removed volume still monitored: %+v", book)
	}

	// The per-volume monitor toggle flips the same flag.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/monitor", v1),
		map[string]bool{"monitored": true}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", v1), nil, &book), http.StatusOK)
	if !book.Monitored {
		t.Fatalf("volume not monitored after monitor toggle: %+v", book)
	}
}

// TestMangaVolumeRemoveOwned: removing an OWNED volume forgets its file
// records so it's no longer owned (it moves to Missing), even without
// deleting the file from disk — the previous behavior left it showing as
// owned.
func TestMangaVolumeRemoveOwned(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 3
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500"}, &series), http.StatusCreated)
	v1 := series.Volumes[0].ID

	// A monochrome manga root with volume 1 on disk.
	root := t.TempDir()
	cbz := filepath.Join(root, "Berserk", "Berserk v01.cbz")
	if err := os.MkdirAll(filepath.Dir(cbz), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cbz, []byte("pages"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "variant": "mono", "path": root}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	var book library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", v1), nil, &book), http.StatusOK)
	if !book.HasFile || !book.HasMonoFile {
		t.Fatalf("volume 1 not owned after scan: %+v", book)
	}

	// Remove from library WITHOUT deleting files: it must stop being owned
	// (records forgotten) and unmonitor — but the file stays on disk.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "manga", "member": false}, &book), http.StatusOK)
	if book.HasFile || book.HasMonoFile || book.Monitored {
		t.Fatalf("removed volume still owned/monitored: %+v", book)
	}
	if _, err := os.Stat(cbz); err != nil {
		t.Fatalf("file should remain on disk when delete-files is off: %v", err)
	}

	// A scan re-finds the on-disk file (owned again) — like any other library.
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", v1), nil, &book), http.StatusOK)
	if !book.HasFile {
		t.Fatal("scan should re-find the on-disk file")
	}

	// Now remove WITH delete-files: the file leaves disk too.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "manga", "member": false, "deleteFiles": true}, &book), http.StatusOK)
	if book.HasFile {
		t.Fatalf("volume still owned after delete-files removal: %+v", book)
	}
	if _, err := os.Stat(cbz); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted from disk: %v", err)
	}
}

// TestComicIssueLibrary: comic issues get the same library flow as manga
// volumes — PUT /book/{id}/library with library=comic unmonitors an issue and
// forgets its file records (it drops to the series' Missing section, the file
// stays on disk), and member=true monitors it back.
func TestComicIssueLibrary(t *testing.T) {
	a := newTestAPI(t, nil)
	issues := 3
	a.mgr.SetSeries(fakeComicProvider{fakeSeriesProvider{volumes: &issues}})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "comic", "foreignSeriesId": "500"}, &series), http.StatusCreated)
	if len(series.Volumes) != 3 || series.Volumes[0].Monitored {
		t.Fatalf("series after add = %+v (issues must start unmonitored)", series.Volumes)
	}
	v1 := series.Volumes[0].ID

	// Monitor issue 1 from Missing (metadata-only adds start everything there).
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "comic", "member": true}, nil), http.StatusOK)

	// A comic root with issue 1 on disk.
	root := t.TempDir()
	cbz := filepath.Join(root, "Berserk", "Berserk v01.cbz")
	if err := os.MkdirAll(filepath.Dir(cbz), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cbz, []byte("pages"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "comic", "path": root}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	var book library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", v1), nil, &book), http.StatusOK)
	if !book.HasFile {
		t.Fatalf("issue 1 not owned after scan: %+v", book)
	}

	// Remove WITHOUT deleting files: unmonitored, no longer owned, file stays.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "comic", "member": false}, &book), http.StatusOK)
	if book.HasFile || book.Monitored {
		t.Fatalf("removed issue still owned/monitored: %+v", book)
	}
	if _, err := os.Stat(cbz); err != nil {
		t.Fatalf("file should remain on disk when delete-files is off: %v", err)
	}

	// Monitor it back from Missing.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", v1),
		map[string]any{"library": "comic", "member": true}, &book), http.StatusOK)
	if !book.Monitored {
		t.Fatalf("re-added issue not monitored: %+v", book)
	}
}

// TestSeriesSearchWanted: the series page's Search wanted sweeps only that
// series' monitored, unowned volumes.
func TestSeriesSearchWanted(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 3
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500", "monitored": true}, &series), http.StatusCreated)

	// Added with explicit monitoring: all 3 volumes are monitored and unowned
	// → all wanted. No indexers are configured, so nothing is grabbed, but
	// every wanted volume is searched.
	var res struct {
		Searched int `json:"searched"`
		Grabbed  int `json:"grabbed"`
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/search", series.ID), nil, &res), http.StatusOK)
	if res.Searched != 3 || res.Grabbed != 0 {
		t.Fatalf("series search = %+v, want searched 3 grabbed 0", res)
	}

	// Unmonitor one volume → it's no longer wanted, so only 2 are searched.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/monitor", series.Volumes[0].ID),
		map[string]bool{"monitored": false}, nil), http.StatusOK)
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/search", series.ID), nil, &res), http.StatusOK)
	if res.Searched != 2 {
		t.Fatalf("after unmonitor, searched = %d, want 2", res.Searched)
	}
}

func jpegBytes(tag string) []byte { return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte(tag)...) }

func writeCBZWithCover(t *testing.T, path string, cover []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("001.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(cover); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func (a *testAPI) rawGet(path string) (*http.Response, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest("GET", a.srv.URL+path, nil)
	req.Header.Set("X-Api-Key", a.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// TestBookCoverCache: the cover is extracted from the owned archive, cached
// on disk, served from cache even if the source vanishes, and refreshed when
// the source file changes.
func TestBookCoverCache(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 1
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500"}, &series), http.StatusCreated)
	v1 := series.Volumes[0].ID

	root := t.TempDir()
	cbz := filepath.Join(root, "Berserk", "Berserk v01.cbz")
	writeCBZWithCover(t, cbz, jpegBytes("COVER-A"))
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "variant": "mono", "path": root}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	cover := fmt.Sprintf("/api/v1/book/%d/cover", v1)

	// Manga defaults to the provider's cover art: extraction 404s until the
	// library is switched to file covers.
	resp, _ := a.rawGet(cover)
	a.want(resp, http.StatusNotFound)
	a.want(a.call("PUT", "/api/v1/settings/metadata",
		map[string]any{"mangaCoverSource": "file"}, nil), http.StatusOK)

	// First fetch extracts the cover and caches it.
	resp, body := a.rawGet(cover)
	a.want(resp, http.StatusOK)
	if !bytes.Equal(body, jpegBytes("COVER-A")) {
		t.Fatalf("cover = %q, want COVER-A", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", ct)
	}

	// Cache hit: even with the source archive gone, the cover still serves.
	if err := os.Remove(cbz); err != nil {
		t.Fatal(err)
	}
	resp, body = a.rawGet(cover)
	a.want(resp, http.StatusOK)
	if !bytes.Equal(body, jpegBytes("COVER-A")) {
		t.Fatalf("cached cover = %q, want COVER-A", body)
	}

	// Invalidation: a newer source file with a different cover refreshes it.
	writeCBZWithCover(t, cbz, jpegBytes("COVER-B"))
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(cbz, future, future); err != nil {
		t.Fatal(err)
	}
	resp, body = a.rawGet(cover)
	a.want(resp, http.StatusOK)
	if !bytes.Equal(body, jpegBytes("COVER-B")) {
		t.Fatalf("after source change, cover = %q, want COVER-B", body)
	}
}

// TestClearDescriptionsAndAllCache: descriptions blank out (and a full-cache
// clear reports images + descriptions together).
func TestClearDescriptionsAndAllCache(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	if author.Description == "" {
		t.Fatal("added author should have a description from the provider")
	}

	// Clear descriptions: the author's description blanks out.
	var res struct {
		DescriptionsCleared int `json:"descriptionsCleared"`
	}
	a.want(a.call("DELETE", "/api/v1/settings/metadata/descriptions", nil, &res), http.StatusOK)
	if res.DescriptionsCleared < 1 {
		t.Fatalf("descriptionsCleared = %d, want >= 1", res.DescriptionsCleared)
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d", author.ID), nil, &author), http.StatusOK)
	if author.Description != "" {
		t.Fatalf("description not cleared: %q", author.Description)
	}

	// Clear-all returns the combined shape (nothing to clear now, but valid).
	var all struct {
		Removed             int   `json:"removed"`
		FreedBytes          int64 `json:"freedBytes"`
		DescriptionsCleared int   `json:"descriptionsCleared"`
	}
	a.want(a.call("DELETE", "/api/v1/cache", nil, &all), http.StatusOK)
}

func TestScanFlow(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Root folder with one matching and one stray ebook.
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "Terry Pratchett"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("Terry Pratchett", "The Colour of Magic.epub"),
		"Stray Book.epub",
	} {
		if err := os.WriteFile(filepath.Join(rootDir, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)

	var result struct {
		Scanned   int `json:"scanned"`
		Matched   int `json:"matched"`
		Unmatched int `json:"unmatched"`
	}
	a.want(a.call("POST", "/api/v1/library/scan", nil, &result), http.StatusOK)
	if result.Scanned != 2 || result.Matched != 1 || result.Unmatched != 1 {
		t.Fatalf("scan result = %+v", result)
	}

	// hasFile shows up in listings; the file appears in book detail.
	var books []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &books), http.StatusOK)
	var tcom library.Book
	for _, b := range books {
		if b.Title == "The Colour of Magic" {
			tcom = b
		}
	}
	if !tcom.HasFile {
		t.Error("matched book should report hasFile")
	}
	var detail library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", tcom.ID), nil, &detail), http.StatusOK)
	if len(detail.Files) != 1 || detail.Files[0].Format != "epub" {
		t.Errorf("book detail files = %+v", detail.Files)
	}

	// Unmatched files are listable; bad filters rejected.
	var unmatched []library.BookFile
	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 1 || filepath.Base(unmatched[0].Path) != "Stray Book.epub" {
		t.Errorf("unmatched = %+v", unmatched)
	}
	a.want(a.call("GET", "/api/v1/bookfile", nil, nil), http.StatusBadRequest)
}

// TestExistingFileImport: the white-glove path for scanned-but-unmatched
// files. A filename that singles out one of the author's books is a confident
// match — imported by the bulk endpoint, which enrolls and monitors the book;
// ambiguous or authorless files are left for review with candidates offered
// (books already owned in the format excluded).
func TestExistingFileImport(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	rootDir := t.TempDir()
	for _, rel := range []string{
		filepath.Join("Terry Pratchett", "Mort - retail.epub"), // confident → Mort
		filepath.Join("Terry Pratchett", "Unknown Novel.epub"), // no match → review
		filepath.Join("Nobody Special", "Some Thing.epub"),     // unknown author
	} {
		path := filepath.Join(rootDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	type option struct {
		File       library.BookFile `json:"file"`
		AuthorName string           `json:"authorName"`
		AuthorID   int64            `json:"authorId"`
		Suggested  int64            `json:"suggested"`
		Confident  bool             `json:"confident"`
		Confidence int              `json:"confidence"`
		Candidates []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"candidates"`
	}
	byBase := func(opts []option) map[string]option {
		m := map[string]option{}
		for _, o := range opts {
			m[filepath.Base(o.File.Path)] = o
		}
		return m
	}

	var options []option
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=ebook", nil, &options), http.StatusOK)
	if len(options) != 3 {
		t.Fatalf("options = %d, want 3", len(options))
	}
	opts := byBase(options)

	mort := opts["Mort - retail.epub"]
	if !mort.Confident || mort.Suggested == 0 || mort.AuthorID != author.ID {
		t.Fatalf("Mort option = %+v", mort)
	}
	if len(mort.Candidates) != 2 {
		t.Errorf("Mort candidates = %+v, want the author's 2 unowned books", mort.Candidates)
	}
	// A unique match rates well above the ambiguity band; no-match rates 0.
	if mort.Confidence < 55 || mort.Confidence > 100 {
		t.Errorf("Mort confidence = %d, want 55–100", mort.Confidence)
	}
	if unknown := opts["Unknown Novel.epub"]; unknown.Confident || unknown.Suggested != 0 ||
		unknown.Confidence != 0 || len(unknown.Candidates) != 2 {
		t.Errorf("Unknown Novel option = %+v, want unconfident/0%% with candidates", unknown)
	}
	if nobody := opts["Some Thing.epub"]; nobody.Confident || nobody.AuthorID != 0 ||
		nobody.Confidence != 0 || len(nobody.Candidates) != 0 {
		t.Errorf("unknown-author option = %+v", nobody)
	}

	// Bulk import takes the confident one only.
	var bulk struct {
		Imported    int `json:"imported"`
		NeedsReview int `json:"needsReview"`
	}
	a.want(a.call("POST", "/api/v1/bookfile/import-matched",
		map[string]string{"mediaType": "ebook"}, &bulk), http.StatusOK)
	if bulk.Imported != 1 || bulk.NeedsReview != 2 {
		t.Fatalf("bulk = %+v", bulk)
	}

	// Mort is now owned, enrolled in the ebook library, and monitored.
	var detail library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", mort.Suggested), nil, &detail), http.StatusOK)
	if !detail.HasEbookFile || !detail.InEbookLibrary || !detail.EbookMonitored {
		t.Fatalf("adopted book = hasFile %v inLibrary %v monitored %v",
			detail.HasEbookFile, detail.InEbookLibrary, detail.EbookMonitored)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("adopted book files = %+v", detail.Files)
	}

	// The two unresolved files remain for review.
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=ebook", nil, &options), http.StatusOK)
	if len(options) != 2 {
		t.Errorf("options after bulk = %d, want 2", len(options))
	}
}

// TestExistingFileFuzzySuggestion: a filename that no title key is a substring
// of — a couple of typos — still gets pre-filled with the closest book by fuzzy
// similarity, but stays UNconfident so it's never auto-imported (the user
// confirms in the picker).
func TestExistingFileFuzzySuggestion(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	rootDir := t.TempDir()
	// "The Colur of Magik" — two typos off "The Colour of Magic"; substring
	// matching can't place it, fuzzy should.
	path := filepath.Join(rootDir, "Terry Pratchett", "The Colur of Magik.epub")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	type option struct {
		File       library.BookFile `json:"file"`
		Suggested  int64            `json:"suggested"`
		Confident  bool             `json:"confident"`
		Confidence int              `json:"confidence"`
		Candidates []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"candidates"`
	}
	var options []option
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=ebook", nil, &options), http.StatusOK)
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1", len(options))
	}
	opt := options[0]

	// The fuzzy pick is the typo's real book, offered but not auto-imported.
	if opt.Confident {
		t.Error("fuzzy match must not be confident (would auto-import a guess)")
	}
	var colourID int64
	for _, c := range opt.Candidates {
		if c.Title == "The Colour of Magic" {
			colourID = c.ID
		}
	}
	if colourID == 0 {
		t.Fatalf("expected 'The Colour of Magic' among candidates: %+v", opt.Candidates)
	}
	if opt.Suggested != colourID {
		t.Errorf("fuzzy Suggested = %d, want The Colour of Magic (%d)", opt.Suggested, colourID)
	}

	// A confident (substring/exact) match is unaffected — sanity that the
	// fuzzy path didn't displace the normal one: the bulk import skips this
	// unconfident guess.
	var bulk struct {
		Imported    int `json:"imported"`
		NeedsReview int `json:"needsReview"`
	}
	a.want(a.call("POST", "/api/v1/bookfile/import-matched",
		map[string]string{"mediaType": "ebook"}, &bulk), http.StatusOK)
	if bulk.Imported != 0 || bulk.NeedsReview != 1 {
		t.Errorf("bulk = %+v, want a fuzzy guess left for review, not imported", bulk)
	}
}

// TestDuplicateUnmatchedFile: an unmatched file that confidently matches an
// OWNED book is flagged as a duplicate with the current library file attached;
// Replace swaps the library's copy for the new file (old one deleted from
// disk), and dismiss?deleteFiles=true deletes the stray from disk instead.
func TestDuplicateUnmatchedFile(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	rootDir := t.TempDir()
	write := func(rel, content string) string {
		path := filepath.Join(rootDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	owned := write("Terry Pratchett/The Colour of Magic.epub", "old-copy")
	write("Terry Pratchett/The Colour of Magic - retail.epub", "better-copy")

	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	// The exact-named file matched; the retail copy is an unmatched duplicate.
	type option struct {
		File      library.BookFile `json:"file"`
		Duplicate *struct {
			BookID     int64            `json:"bookId"`
			Title      string           `json:"title"`
			File       library.BookFile `json:"file"`
			Confidence int              `json:"confidence"`
		} `json:"duplicate"`
	}
	var options []option
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=ebook", nil, &options), http.StatusOK)
	if len(options) != 1 {
		t.Fatalf("options = %d, want 1", len(options))
	}
	dup := options[0]
	if dup.Duplicate == nil || dup.Duplicate.Title != "The Colour of Magic" ||
		dup.Duplicate.File.Path != owned || dup.Duplicate.Confidence == 0 {
		t.Fatalf("duplicate = %+v", dup.Duplicate)
	}

	// Replace: the stray becomes the book's file; the old copy leaves disk.
	var replaced struct {
		File         library.BookFile `json:"file"`
		DeletedFiles int              `json:"deletedFiles"`
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/bookfile/%d/replace", dup.File.ID),
		map[string]int64{"bookId": dup.Duplicate.BookID}, &replaced), http.StatusOK)
	if replaced.File.BookID != dup.Duplicate.BookID || replaced.DeletedFiles != 1 {
		t.Fatalf("replace = %+v", replaced)
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Error("old library copy still on disk after replace")
	}
	// The new file was organized into the template spot with its content.
	var detail library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", dup.Duplicate.BookID), nil, &detail), http.StatusOK)
	if len(detail.Files) != 1 {
		t.Fatalf("book files after replace = %+v", detail.Files)
	}
	data, err := os.ReadFile(detail.Files[0].Path)
	if err != nil || string(data) != "better-copy" {
		t.Fatalf("replaced file content = %q (%v)", data, err)
	}

	// Second stray duplicate: resolve it the other way — delete from disk.
	stray := write("Terry Pratchett/The Colour of Magic - another.epub", "junk-copy")
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=ebook", nil, &options), http.StatusOK)
	if len(options) != 1 || options[0].Duplicate == nil {
		t.Fatalf("second duplicate not detected: %+v", options)
	}
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/bookfile/%d?deleteFiles=true", options[0].File.ID), nil, nil), http.StatusOK)
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("stray still on disk after delete")
	}
	// Library copy untouched.
	if _, err := os.Stat(detail.Files[0].Path); err != nil {
		t.Errorf("library copy vanished: %v", err)
	}
}

// TestExistingFileImportSeries: the existing-file import flow for the
// series-first libraries. Manga: a "Series vNN" file confidently matches its
// series' volume and bulk-imports; unknown series are left for review with the
// parsed name offered. Magazine: once the magazine exists, a dated file is a
// confident match and the manual import materializes the issue (organize-only
// magazines still import files already on disk).
func TestExistingFileImportSeries(t *testing.T) {
	a := newTestAPI(t, nil)
	volumes := 3
	a.mgr.SetSeries(fakeSeriesProvider{volumes: &volumes})

	// --- Manga ---
	// The scan auto-matches exact "Series vNN" layouts on its own; the options
	// flow covers what it can't — here a scene-style folder ("Berserk (Dark
	// Horse)") that only fuzzy-matches the series.
	mangaRoot := t.TempDir()
	for _, rel := range []string{
		filepath.Join("Berserk (Dark Horse)", "Berserk v02.cbz"),
		filepath.Join("Mystery Series", "Mystery v01.cbz"),
	} {
		path := filepath.Join(mangaRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "path": mangaRoot}, nil), http.StatusCreated)
	var series library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "manga", "foreignSeriesId": "500"}, &series), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	type option struct {
		File       library.BookFile `json:"file"`
		SeriesName string           `json:"seriesName"`
		SeriesID   int64            `json:"seriesId"`
		Volume     float64          `json:"volume"`
		Issue      string           `json:"issue"`
		Suggested  int64            `json:"suggested"`
		Confident  bool             `json:"confident"`
		Confidence int              `json:"confidence"`
	}
	var options []option
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=manga", nil, &options), http.StatusOK)
	if len(options) != 2 {
		t.Fatalf("manga options = %d, want 2", len(options))
	}
	for _, o := range options {
		base := filepath.Base(o.File.Path)
		switch base {
		case "Berserk v02.cbz":
			if !o.Confident || o.SeriesID != series.ID || o.Volume != 2 || o.Suggested == 0 || o.Confidence < 80 {
				t.Errorf("Berserk option = %+v", o)
			}
		case "Mystery v01.cbz":
			if o.Confident || o.SeriesID != 0 || o.SeriesName != "Mystery Series" {
				t.Errorf("Mystery option = %+v", o)
			}
		}
	}

	var bulk struct {
		Imported    int `json:"imported"`
		NeedsReview int `json:"needsReview"`
	}
	a.want(a.call("POST", "/api/v1/bookfile/import-matched",
		map[string]string{"mediaType": "manga"}, &bulk), http.StatusOK)
	if bulk.Imported != 1 || bulk.NeedsReview != 1 {
		t.Fatalf("manga bulk = %+v", bulk)
	}
	var detail library.Series
	a.want(a.call("GET", fmt.Sprintf("/api/v1/series/%d", series.ID), nil, &detail), http.StatusOK)
	owned := 0
	for _, v := range detail.Volumes {
		if v.HasFile {
			owned++
		}
	}
	if owned != 1 {
		t.Fatalf("owned volumes after import = %d, want 1", owned)
	}

	// --- Magazine ---
	// A fuzzy folder name ("Wired Magazine" vs the magazine "Wired"): the
	// scan's exact matcher can't place it — the options flow can. (Exact names
	// auto-match the moment the magazine is added, via rematch.)
	magRoot := t.TempDir()
	magFile := filepath.Join(magRoot, "Wired Magazine", "Wired Magazine - 2026-05-01.pdf")
	if err := os.MkdirAll(filepath.Dir(magFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(magFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "magazine", "path": magRoot}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	// Unknown magazine: the parsed name is offered, nothing confident.
	options = nil
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=magazine", nil, &options), http.StatusOK)
	if len(options) != 1 || options[0].Confident || options[0].SeriesName != "Wired Magazine" {
		t.Fatalf("magazine options = %+v", options)
	}
	fileID := options[0].File.ID

	// Add the magazine by name (the UI's "+ Add magazine" button) → confident
	// (80: the folder name is a fuzzy, not exact, title match).
	var mag library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "magazine", "title": "Wired"}, &mag), http.StatusCreated)
	options = nil
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=magazine", nil, &options), http.StatusOK)
	if len(options) != 1 || !options[0].Confident || options[0].SeriesID != mag.ID ||
		options[0].Issue != "2026-05-01" || options[0].Confidence < 75 {
		t.Fatalf("magazine options after add = %+v", options)
	}

	// Manual import materializes the issue and adopts the file.
	a.want(a.call("POST", fmt.Sprintf("/api/v1/bookfile/%d/match", fileID),
		map[string]any{"seriesId": mag.ID, "issue": "2026-05-01"}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/series/%d", mag.ID), nil, &detail), http.StatusOK)
	if len(detail.Volumes) != 1 || !detail.Volumes[0].HasFile {
		t.Fatalf("magazine issues after import = %+v", detail.Volumes)
	}
	options = nil
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=magazine", nil, &options), http.StatusOK)
	if len(options) != 0 {
		t.Errorf("magazine options after import = %+v, want none", options)
	}
}

func TestNamingSettingsAndRename(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Background timing settings: zero = default, set values clamp to range.
	var ts struct {
		SearchIntervalHours   int `json:"searchIntervalHours"`
		ImportIntervalSeconds int `json:"importIntervalSeconds"`
	}
	a.want(a.call("GET", "/api/v1/settings/timings", nil, &ts), http.StatusOK)
	if ts.SearchIntervalHours != 0 {
		t.Fatalf("timings default = %+v, want zeros (use-default)", ts)
	}
	a.want(a.call("PUT", "/api/v1/settings/timings",
		map[string]int{"searchIntervalHours": 2, "importIntervalSeconds": 5}, &ts), http.StatusOK)
	if ts.SearchIntervalHours != 2 || ts.ImportIntervalSeconds != 30 {
		t.Fatalf("timings after put = %+v (import should clamp 5 -> 30)", ts)
	}

	// Defaults come with tokens and a rendered example.
	var ns struct {
		EbookFolder string   `json:"ebookFolder"`
		EbookFile   string   `json:"ebookFile"`
		Tokens      []string `json:"tokens"`
		Example     string   `json:"example"`
	}
	a.want(a.call("GET", "/api/v1/settings/naming", nil, &ns), http.StatusOK)
	if ns.EbookFolder != "{Author Name}/{Book Title} ({Release Year})" || len(ns.Tokens) == 0 {
		t.Fatalf("naming defaults = %+v", ns)
	}
	if ns.Example != "Terry Pratchett/The Colour of Magic (1983)/Terry Pratchett - Discworld 1 - The Colour of Magic (1983).epub" {
		t.Fatalf("example = %q", ns.Example)
	}

	// Empty templates fall back to defaults rather than erroring.
	var filled struct {
		EbookFolder string `json:"ebookFolder"`
	}
	a.want(a.call("PUT", "/api/v1/settings/naming",
		map[string]string{"ebookFolder": "", "ebookFile": "x"}, &filled), http.StatusOK)
	if filled.EbookFolder != "{Author Name}/{Book Title} ({Release Year})" {
		t.Fatalf("empty folder template not defaulted: %+v", filled)
	}
	a.want(a.call("PUT", "/api/v1/settings/naming", map[string]string{
		"ebookFolder": "{Author SortName}",
		"ebookFile":   "{Book Title} ({Release Year})",
	}, &ns), http.StatusOK)
	if ns.Example != "Pratchett, Terry/The Colour of Magic (1983).epub" {
		t.Fatalf("updated example = %q", ns.Example)
	}

	// Set up a real file, misplaced, matched to a book.
	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "wrong-name.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	// The scan can't match "wrong-name" — import it manually.
	var unmatched []library.BookFile
	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 1 {
		t.Fatalf("unmatched = %+v", unmatched)
	}
	var imported struct {
		File  library.BookFile `json:"file"`
		Skips []string         `json:"skips"`
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/bookfile/%d/match", unmatched[0].ID),
		map[string]int64{"bookId": book.ID}, &imported), http.StatusOK)
	if imported.File.BookID != book.ID {
		t.Fatalf("imported file = %+v", imported)
	}
	// Manual import moved it straight into the template location.
	wantPath := filepath.Join(rootDir, "Pratchett, Terry", "The Colour of Magic (1983).epub")
	if imported.File.Path != wantPath {
		t.Fatalf("imported path = %q, want %q", imported.File.Path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("file not on disk at target: %v", err)
	}

	// Rename preview now reports nothing to do.
	var preview struct {
		Moves []map[string]any `json:"moves"`
	}
	a.want(a.call("GET", "/api/v1/library/rename", nil, &preview), http.StatusOK)
	if len(preview.Moves) != 0 {
		t.Fatalf("preview after import = %+v", preview.Moves)
	}

	// Changing templates makes the preview propose a move; apply executes it.
	a.want(a.call("PUT", "/api/v1/settings/naming", map[string]string{
		"ebookFolder": "{Author Name}",
		"ebookFile":   "{Book Title}",
	}, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/library/rename", nil, &preview), http.StatusOK)
	if len(preview.Moves) != 1 {
		t.Fatalf("preview after template change = %+v", preview.Moves)
	}

	// A library page's Organize is scoped: its own library sees the move,
	// another library sees none — other media types' files never move.
	a.want(a.call("GET", "/api/v1/library/rename?mediaType=ebook", nil, &preview), http.StatusOK)
	if len(preview.Moves) != 1 {
		t.Fatalf("ebook-scoped preview = %d moves, want 1", len(preview.Moves))
	}
	a.want(a.call("GET", "/api/v1/library/rename?mediaType=manga", nil, &preview), http.StatusOK)
	if len(preview.Moves) != 0 {
		t.Fatalf("manga-scoped preview sees the ebook move: %+v", preview.Moves)
	}
	a.want(a.call("GET", "/api/v1/library/rename?mediaType=bogus", nil, nil), http.StatusBadRequest)
	a.want(a.call("GET", "/api/v1/library/rename", nil, &preview), http.StatusOK) // re-plan unscoped

	var applied struct {
		Moves []map[string]any `json:"moves"`
		Skips []string         `json:"skips"`
	}
	a.want(a.call("POST", "/api/v1/library/rename", nil, &applied), http.StatusOK)
	if len(applied.Moves) != 1 || len(applied.Skips) != 0 {
		t.Fatalf("apply = %+v", applied)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "Terry Pratchett", "The Colour of Magic.epub")); err != nil {
		t.Fatalf("file not at new target: %v", err)
	}
	// Old author dir swept.
	if _, err := os.Stat(filepath.Join(rootDir, "Pratchett, Terry")); !os.IsNotExist(err) {
		t.Error("old folder not swept")
	}
}

func TestAddingBookRematchesScannedFiles(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Scan finds the file before its book exists.
	rootDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootDir, "Terry Pratchett"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rootDir, "Terry Pratchett", "The Colour of Magic.epub")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	var unmatched []library.BookFile
	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 1 {
		t.Fatalf("unmatched = %+v", unmatched)
	}

	// Adding the book attaches the file with no re-scan.
	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)

	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 0 {
		t.Fatalf("file still unmatched after adding its book: %+v", unmatched)
	}
	var detail library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", book.ID), nil, &detail), http.StatusOK)
	if !detail.HasFile || len(detail.Files) != 1 {
		t.Fatalf("book after add = hasFile %v, files %+v", detail.HasFile, detail.Files)
	}
}

func TestDismissBookFile(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "junk.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": rootDir}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	var unmatched []library.BookFile
	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 1 {
		t.Fatalf("unmatched = %+v", unmatched)
	}
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/bookfile/%d", unmatched[0].ID), nil, nil), http.StatusNoContent)
	a.want(a.call("GET", "/api/v1/bookfile?unmatched=true", nil, &unmatched), http.StatusOK)
	if len(unmatched) != 0 {
		t.Error("dismissed file still listed")
	}
	// Disk untouched.
	if _, err := os.Stat(filepath.Join(rootDir, "junk.epub")); err != nil {
		t.Errorf("dismiss must not delete from disk: %v", err)
	}
	// Match with a bogus book id is rejected cleanly.
	a.want(a.call("POST", "/api/v1/bookfile/1/match", map[string]int64{"bookId": 999}, nil), http.StatusNotFound)
}

// mockTorznab serves a minimal caps + one-release search response.
func mockTorznab(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Query().Get("t") {
		case "caps":
			w.Write([]byte(`<caps><server title="mock"/></caps>`))
		case "search":
			w.Write([]byte(`<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item>
				<title>Mort epub</title><guid>g1</guid><link>https://mock/dl/1</link>
				<torznab:attr name="seeders" value="5"/><torznab:attr name="size" value="1048576"/>
			</item></channel></rss>`))
		default:
			http.Error(w, "bad t", http.StatusBadRequest)
		}
	}))
}

func TestIndexerCRUDAndReleaseSearch(t *testing.T) {
	a := newTestAPI(t, nil)
	srv := mockTorznab(t)
	defer srv.Close()

	// Validation.
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]string{"name": "x", "type": "gopher", "baseUrl": srv.URL}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]string{"name": "x", "type": "torznab", "baseUrl": "ftp://nope"}, nil), http.StatusBadRequest)

	// Test-before-save against the mock endpoint.
	a.want(a.call("POST", "/api/v1/indexer/test",
		map[string]any{"name": "mock", "type": "torznab", "baseUrl": srv.URL}, nil), http.StatusOK)

	// Create, list, update, search, delete.
	var ind struct {
		ID       int64  `json:"id"`
		Priority int    `json:"priority"`
		Enabled  bool   `json:"enabled"`
		Name     string `json:"name"`
	}
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]any{"name": "mock", "type": "torznab", "baseUrl": srv.URL, "enabled": true}, &ind), http.StatusCreated)
	if ind.ID == 0 || ind.Priority != 25 {
		t.Fatalf("created indexer = %+v", ind)
	}

	var list []map[string]any
	a.want(a.call("GET", "/api/v1/indexer", nil, &list), http.StatusOK)
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}

	var result struct {
		Releases []map[string]any `json:"releases"`
		Errors   []string         `json:"errors"`
	}
	a.want(a.call("GET", "/api/v1/release?term=mort", nil, &result), http.StatusOK)
	if len(result.Releases) != 1 || len(result.Errors) != 0 {
		t.Fatalf("release search = %+v", result)
	}
	if result.Releases[0]["title"] != "Mort epub" || result.Releases[0]["protocol"] != "torrent" {
		t.Errorf("release = %+v", result.Releases[0])
	}
	// Generic scoring fields present: epub recognized and approved.
	if result.Releases[0]["approved"] != true {
		t.Errorf("release not approved: %+v", result.Releases[0])
	}
	a.want(a.call("GET", "/api/v1/release", nil, nil), http.StatusBadRequest)

	// Disable it: searches now hit zero indexers.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/indexer/%d", ind.ID),
		map[string]any{"name": "mock", "type": "torznab", "baseUrl": srv.URL, "enabled": false}, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/release?term=mort", nil, &result), http.StatusOK)
	if len(result.Releases) != 0 {
		t.Errorf("disabled indexer still searched: %+v", result)
	}

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/indexer/%d", ind.ID), nil, nil), http.StatusNoContent)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/indexer/%d", ind.ID), nil, nil), http.StatusNotFound)
}

func TestDownloadClientsAndGrab(t *testing.T) {
	a := newTestAPI(t, nil)

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
			w.Write([]byte(`{"queue": {"slots": [{"nzo_id": "nzo_1", "filename": "Mort", "status": "Downloading", "percentage": "50", "cat": "librinode"}]}}`))
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
	if client.Category != "librinode" || client.Priority != 1 {
		t.Fatalf("client defaults = %+v", client)
	}

	// Grab routes by protocol; no torrent client exists.
	var grab download.GrabResult
	a.want(a.call("POST", "/api/v1/release/grab",
		map[string]any{"title": "Mort", "downloadUrl": "https://idx/get/1.nzb", "protocol": "usenet"}, &grab), http.StatusOK)
	if grab.Client != "sab" || grab.ID != "nzo_1" {
		t.Fatalf("grab = %+v", grab)
	}
	a.want(a.call("POST", "/api/v1/release/grab",
		map[string]any{"title": "Mort", "downloadUrl": "magnet:?xt=x", "protocol": "torrent"}, nil), http.StatusServiceUnavailable)
	a.want(a.call("POST", "/api/v1/release/grab",
		map[string]any{"title": "Mort", "downloadUrl": "x", "protocol": "carrier-pigeon"}, nil), http.StatusBadRequest)

	// Queue shows the download.
	var queue struct {
		Items []download.Item `json:"items"`
	}
	a.want(a.call("GET", "/api/v1/queue", nil, &queue), http.StatusOK)
	if len(queue.Items) != 1 || queue.Items[0].Status != "downloading" || queue.Items[0].Progress != 0.5 {
		t.Fatalf("queue = %+v", queue.Items)
	}

	// Disable → grab loses its client.
	client.Enabled = false
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/downloadclient/%d", client.ID), client, nil), http.StatusOK)
	a.want(a.call("POST", "/api/v1/release/grab",
		map[string]any{"title": "Mort", "downloadUrl": "https://idx/get/1.nzb", "protocol": "usenet"}, nil), http.StatusServiceUnavailable)

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/downloadclient/%d", client.ID), nil, nil), http.StatusNoContent)
}

// TestProwlarrSyncFlow simulates the conversation Prowlarr has with a
// TestProwlarrCapabilityResources: the endpoints Prowlarr's Readarr proxy
// reads during application sync must return Readarr-shaped resources. A null
// field (or a 404 on Readarr's metadataprofile endpoint) made Prowlarr throw
// a NullReferenceException in BuildReadarrIndexer and refuse to sync torrent
// indexers — the browser UI keeps its native shape.
func TestProwlarrCapabilityResources(t *testing.T) {
	a := newTestAPI(t, nil)
	const ua = "Prowlarr/1.30.2.4939 (windows 10.0)"

	// metadataprofile is Readarr-only; a 404 here was the NRE root cause.
	var mps []map[string]any
	a.want(a.call("GET", "/api/v1/metadataprofile", nil, &mps), http.StatusOK)
	if len(mps) == 0 || mps[0]["name"] == "" || mps[0]["id"] == nil {
		t.Fatalf("metadataprofile = %+v (want a named default profile)", mps)
	}

	// A torrent download client so Prowlarr can detect torrent support — the
	// resource must carry protocol=torrent (without it Prowlarr syncs usenet
	// indexers only).
	a.want(a.call("POST", "/api/v1/downloadclient", map[string]any{
		"name": "qbit", "type": "qbittorrent", "host": "http://localhost:8080",
		"username": "u", "password": "p",
	}, nil), http.StatusCreated)
	var dcs []map[string]any
	a.want(a.callUA(ua, "GET", "/api/v1/downloadclient", nil, &dcs), http.StatusOK)
	if len(dcs) != 1 || dcs[0]["protocol"] != "torrent" || dcs[0]["implementation"] != "QBittorrent" {
		t.Fatalf("prowlarr download client = %+v (want protocol torrent)", dcs)
	}
	// The browser UI keeps the native shape (no arr protocol/implementation).
	var nativeDcs []map[string]any
	a.want(a.call("GET", "/api/v1/downloadclient", nil, &nativeDcs), http.StatusOK)
	if _, leaked := nativeDcs[0]["implementation"]; leaked {
		t.Fatalf("native download client leaked the arr shape: %+v", nativeDcs[0])
	}

	// Root folders as Readarr resources: non-null name and defaultTags.
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]any{"mediaType": "ebook", "path": t.TempDir()}, nil), http.StatusCreated)
	var rfs []map[string]any
	a.want(a.callUA(ua, "GET", "/api/v1/rootfolder", nil, &rfs), http.StatusOK)
	if len(rfs) != 1 || rfs[0]["name"] == "" || rfs[0]["defaultTags"] == nil {
		t.Fatalf("prowlarr root folder = %+v (want name + defaultTags)", rfs)
	}

	// The Torznab schema entry must exist (its absence in Prowlarr's cache was
	// what BuildReadarrIndexer dereferenced as null).
	var schema []map[string]any
	a.want(a.call("GET", "/api/v1/indexer/schema", nil, &schema), http.StatusOK)
	var torznab map[string]any
	for _, e := range schema {
		if e["implementation"] == "Torznab" {
			torznab = e
		}
	}
	if torznab == nil || torznab["protocol"] != "torrent" {
		t.Fatalf("schema missing a usable Torznab entry: %+v", schema)
	}
}

// Readarr-type application: status check, schema fetch, then indexer
// add/list/update/delete using arr-style resources with fields[].
func TestProwlarrSyncFlow(t *testing.T) {
	a := newTestAPI(t, nil)

	// 1. Test: status must expose a dotted parseable version.
	var status struct {
		Version    string `json:"version"`
		AppVersion string `json:"appVersion"`
	}
	a.want(a.call("GET", "/api/v1/system/status", nil, &status), http.StatusOK)
	for _, part := range strings.Split(status.Version, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			t.Fatalf("version %q is not a dotted number", status.Version)
		}
	}
	if status.AppVersion == "" {
		t.Error("real app version missing")
	}

	// 2. Schema: both implementations offered.
	var schema []struct {
		Implementation string `json:"implementation"`
		ConfigContract string `json:"configContract"`
	}
	a.want(a.call("GET", "/api/v1/indexer/schema", nil, &schema), http.StatusOK)
	if len(schema) != 2 || schema[0].Implementation != "Newznab" || schema[1].ConfigContract != "TorznabSettings" {
		t.Fatalf("schema = %+v", schema)
	}

	// 3. Tags resolve (empty).
	a.want(a.call("GET", "/api/v1/tag", nil, nil), http.StatusOK)

	// 4. Push an indexer the way Prowlarr does.
	payload := map[string]any{
		"name":                    "MyIndexer (Prowlarr)",
		"implementation":          "Torznab",
		"configContract":          "TorznabSettings",
		"enableRss":               true,
		"enableAutomaticSearch":   true,
		"enableInteractiveSearch": true,
		"priority":                20,
		"fields": []map[string]any{
			{"name": "baseUrl", "value": "http://prowlarr:9696/1/"},
			{"name": "apiPath", "value": "/api"},
			{"name": "apiKey", "value": "prowlarr-key"},
			{"name": "categories", "value": []int{7000, 7010, 7020}},
		},
	}
	var created map[string]any
	a.want(a.call("POST", "/api/v1/indexer", payload, &created), http.StatusCreated)
	id := int64(created["id"].(float64))

	// Stored natively and correctly.
	if created["type"] != "torznab" || created["baseUrl"] != "http://prowlarr:9696/1" ||
		created["categories"] != "7000,7010,7020" || created["apiKey"] != "prowlarr-key" {
		t.Fatalf("created = %+v", created)
	}
	// And the arr view round-trips for Prowlarr's diffing.
	if created["implementation"] != "Torznab" || created["protocol"] != "torrent" {
		t.Fatalf("arr view = %+v", created)
	}
	fields, ok := created["fields"].([]any)
	if !ok || len(fields) == 0 {
		t.Fatalf("fields missing from response: %+v", created)
	}

	// 5. List and get-by-id include both dialects.
	var list []map[string]any
	a.want(a.call("GET", "/api/v1/indexer", nil, &list), http.StatusOK)
	if len(list) != 1 || list[0]["implementation"] != "Torznab" || list[0]["name"] != "MyIndexer (Prowlarr)" {
		t.Fatalf("list = %+v", list)
	}
	var got map[string]any
	a.want(a.call("GET", fmt.Sprintf("/api/v1/indexer/%d", id), nil, &got), http.StatusOK)
	if got["implementation"] != "Torznab" {
		t.Fatalf("get = %+v", got)
	}

	// 6. Prowlarr updates (e.g. disables) the indexer.
	payload["enableRss"] = false
	payload["enableAutomaticSearch"] = false
	payload["enableInteractiveSearch"] = false
	var updated map[string]any
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/indexer/%d", id), payload, &updated), http.StatusOK)
	if updated["enabled"] != false || updated["enableRss"] != false {
		t.Fatalf("updated = %+v", updated)
	}

	// 7. And removes it.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/indexer/%d", id), nil, nil), http.StatusNoContent)

	// Unsupported implementations are rejected cleanly.
	payload["implementation"] = "Omgwtfnzbs"
	a.want(a.call("POST", "/api/v1/indexer", payload, nil), http.StatusBadRequest)
}

func TestQualityProfiles(t *testing.T) {
	a := newTestAPI(t, nil)

	// Seeded default present.
	var profiles []library.QualityProfile
	a.want(a.call("GET", "/api/v1/qualityprofile", nil, &profiles), http.StatusOK)
	if len(profiles) != 1 || profiles[0].Name != "Standard Ebook" || !profiles[0].IsDefault {
		t.Fatalf("profiles = %+v", profiles)
	}
	seeded := profiles[0].ID

	// Create an epub-only profile; validation rejects junk.
	a.want(a.call("POST", "/api/v1/qualityprofile",
		map[string]any{"name": "Bad", "formats": []string{"docx"}}, nil), http.StatusBadRequest)
	var epubOnly library.QualityProfile
	a.want(a.call("POST", "/api/v1/qualityprofile",
		map[string]any{"name": "EPUB Only", "formats": []string{"epub"}, "language": "english"}, &epubOnly), http.StatusCreated)
	if epubOnly.IsDefault {
		t.Error("new profile must not steal default")
	}

	// Default swap and guarded delete.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/qualityprofile/%d/default", epubOnly.ID), nil, nil), http.StatusOK)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/qualityprofile/%d", epubOnly.ID), nil, nil), http.StatusBadRequest)
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/qualityprofile/%d", seeded), nil, nil), http.StatusNoContent)

	// Update the remaining profile.
	var updated library.QualityProfile
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/qualityprofile/%d", epubOnly.ID),
		map[string]any{"name": "EPUB Only", "formats": []string{"epub", "azw3"}, "retailBonus": 30}, &updated), http.StatusOK)
	if len(updated.Formats) != 2 || updated.RetailBonus != 30 || !updated.IsDefault {
		t.Errorf("updated = %+v", updated)
	}
}

func TestProfileDrivesSearchScoring(t *testing.T) {
	a := newTestAPI(t, nil)

	// Mock indexer serving one mobi release.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			w.Write([]byte(`<caps><server title="mock"/></caps>`))
			return
		}
		w.Write([]byte(`<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
			<item><title>Mort MOBI</title><guid>g1</guid><link>https://mock/dl/1</link>
			<torznab:attr name="seeders" value="5"/><torznab:attr name="size" value="1048576"/></item>
		</channel></rss>`))
	}))
	defer srv.Close()
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]any{"name": "mock", "type": "torznab", "baseUrl": srv.URL, "enabled": true}, nil), http.StatusCreated)

	approved := func() bool {
		var result struct {
			Releases []struct {
				Approved   bool     `json:"approved"`
				Rejections []string `json:"rejections"`
			} `json:"releases"`
		}
		a.want(a.call("GET", "/api/v1/release?term=mort", nil, &result), http.StatusOK)
		if len(result.Releases) != 1 {
			t.Fatalf("releases = %+v", result.Releases)
		}
		return result.Releases[0].Approved
	}

	// Seeded default allows mobi.
	if !approved() {
		t.Fatal("mobi should be approved under the standard profile")
	}

	// Switch the default to an epub-only profile: same release now rejected.
	var epubOnly library.QualityProfile
	a.want(a.call("POST", "/api/v1/qualityprofile",
		map[string]any{"name": "EPUB Only", "formats": []string{"epub"}}, &epubOnly), http.StatusCreated)
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/qualityprofile/%d/default", epubOnly.ID), nil, nil), http.StatusOK)
	if approved() {
		t.Fatal("mobi should be rejected under the epub-only profile")
	}
}

func TestBookAwareReleaseSearch(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Indexer returning one right and one wrong release for any query.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			w.Write([]byte(`<caps><server title="mock"/></caps>`))
			return
		}
		w.Write([]byte(`<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>
			<item><title>Terry Pratchett - The Colour of Magic Retail EPUB</title><guid>g1</guid>
				<link>https://mock/dl/1</link>
				<torznab:attr name="seeders" value="9"/><torznab:attr name="size" value="1048576"/></item>
			<item><title>Stephen King - It EPUB</title><guid>g2</guid>
				<link>https://mock/dl/2</link>
				<torznab:attr name="seeders" value="99"/><torznab:attr name="size" value="1048576"/></item>
		</channel></rss>`))
	}))
	defer srv.Close()
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]any{"name": "mock", "type": "torznab", "baseUrl": srv.URL, "enabled": true}, nil), http.StatusCreated)

	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)

	var result struct {
		Releases []struct {
			Title      string   `json:"title"`
			Approved   bool     `json:"approved"`
			Score      int      `json:"score"`
			Rejections []string `json:"rejections"`
		} `json:"releases"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/release?bookId=%d", book.ID), nil, &result), http.StatusOK)
	if len(result.Releases) != 2 {
		t.Fatalf("releases = %+v", result.Releases)
	}
	// The right book ranks first despite fewer seeders; the wrong one is rejected.
	first, second := result.Releases[0], result.Releases[1]
	if !first.Approved || first.Title != "Terry Pratchett - The Colour of Magic Retail EPUB" {
		t.Errorf("first = %+v", first)
	}
	if second.Approved || len(second.Rejections) == 0 {
		t.Errorf("wrong book not rejected: %+v", second)
	}

	a.want(a.call("GET", "/api/v1/release?bookId=9999", nil, nil), http.StatusNotFound)
}

func TestAutoSearchEndpoints(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Indexer offering the right book; SAB accepting grabs.
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			w.Write([]byte(`<caps><server title="mock"/></caps>`))
			return
		}
		w.Write([]byte(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>
			<item><title>Terry Pratchett - The Colour of Magic Retail EPUB</title><guid>g1</guid>
			<link>https://idx/get/tcom.nzb</link><newznab:attr name="size" value="1048576"/></item>
		</channel></rss>`))
	}))
	defer idx.Close()
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "addurl":
			w.Write([]byte(`{"status": true, "nzo_ids": ["nzo_as"]}`))
		default:
			w.Write([]byte(`{"version": "4.3.2", "queue": {"slots": []}, "history": {"slots": []}}`))
		}
	}))
	defer sab.Close()

	a.want(a.call("POST", "/api/v1/indexer",
		map[string]any{"name": "mock", "type": "newznab", "baseUrl": idx.URL, "enabled": true}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "sab", "type": "sabnzbd", "host": sab.URL, "apiKey": "k", "enabled": true}, nil), http.StatusCreated)

	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)

	// Per-book automatic search grabs the release.
	var outcome struct {
		Grabbed bool   `json:"grabbed"`
		Release string `json:"release"`
		Message string `json:"message"`
	}
	a.want(a.call("POST", fmt.Sprintf("/api/v1/book/%d/search", book.ID), nil, &outcome), http.StatusOK)
	if !outcome.Grabbed || outcome.Release == "" {
		t.Fatalf("outcome = %+v", outcome)
	}

	// History has the grab tied to the book (paged shape with a total).
	var history struct {
		Records []download.GrabRecord `json:"records"`
		Total   int                   `json:"total"`
	}
	a.want(a.call("GET", "/api/v1/history", nil, &history), http.StatusOK)
	if history.Total != 1 || len(history.Records) != 1 || history.Records[0].BookID != book.ID {
		t.Fatalf("history = %+v", history)
	}

	// Search-all skips the pending book.
	var all struct {
		Searched int `json:"searched"`
		Grabbed  int `json:"grabbed"`
	}
	a.want(a.call("POST", "/api/v1/library/search", nil, &all), http.StatusOK)
	if all.Searched != 0 || all.Grabbed != 0 {
		t.Fatalf("search-all = %+v", all)
	}

	a.want(a.call("POST", "/api/v1/book/9999/search", nil, nil), http.StatusNotFound)
}

func TestNamingSaveKeepsOtherTemplates(t *testing.T) {
	a := newTestAPI(t, nil)

	// A partial save (only ebook fields, like an older client) must not wipe
	// the other media types' templates.
	var ns struct {
		EbookFolder    string `json:"ebookFolder"`
		MangaFile      string `json:"mangaFile"`
		ComicFile      string `json:"comicFile"`
		MagazineFolder string `json:"magazineFolder"`
	}
	a.want(a.call("PUT", "/api/v1/settings/naming", map[string]string{
		"ebookFolder": "{Author SortName}",
		"ebookFile":   "{Book Title}",
	}, &ns), http.StatusOK)
	if ns.EbookFolder != "{Author SortName}" {
		t.Errorf("ebook folder not saved: %+v", ns)
	}
	if ns.MangaFile == "" || ns.ComicFile == "" || ns.MagazineFolder == "" {
		t.Fatalf("partial naming save wiped other templates: %+v", ns)
	}
}

func TestMagazineSeries(t *testing.T) {
	a := newTestAPI(t, nil)

	// Magazines are created by name; no provider involved.
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "magazine"}, nil), http.StatusBadRequest) // no title
	var mag library.Series
	a.want(a.call("POST", "/api/v1/series",
		map[string]any{"mediaType": "magazine", "title": "The Economist"}, &mag), http.StatusCreated)
	// Adds are metadata-only: the magazine starts unmonitored (no auto-grab
	// until the user flips the series toggle).
	if mag.MediaType != "magazine" || mag.Monitored || mag.MonitorNew || mag.Source != "manual" {
		t.Fatalf("magazine = %+v", mag)
	}
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/series/%d/monitor", mag.ID),
		map[string]bool{"monitored": true, "monitorNew": true}, &mag), http.StatusOK)
	if !mag.Monitored || !mag.MonitorNew {
		t.Fatalf("magazine after monitor toggle = %+v", mag)
	}

	// Listed alongside other series types; filterable.
	var list []library.Series
	a.want(a.call("GET", "/api/v1/series?mediaType=magazine", nil, &list), http.StatusOK)
	if len(list) != 1 || list[0].Title != "The Economist" {
		t.Fatalf("list = %+v", list)
	}

	// Refresh is a quiet no-op (no provider), not an error.
	a.want(a.call("POST", fmt.Sprintf("/api/v1/series/%d/refresh", mag.ID), nil, nil), http.StatusOK)

	// Magazine search-by-provider is rejected with guidance.
	a.want(a.call("GET", "/api/v1/search?term=x&type=magazine", nil, nil), http.StatusBadRequest)

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/series/%d", mag.ID), nil, nil), http.StatusNoContent)
}

func TestLibrariesHomeAndCrossAdd(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// Fresh install: nothing active, Home empty.
	var libs []library.LibraryStatus
	a.want(a.call("GET", "/api/v1/libraries", nil, &libs), http.StatusOK)
	for _, l := range libs {
		if l.Active {
			t.Fatalf("library %s active on fresh install", l.MediaType)
		}
	}

	// Adding a book (default: ebook library) enrolls it but does NOT surface
	// the library — only creating the library (adding a root folder) does,
	// Plex-style.
	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)
	if !book.InEbookLibrary || !book.EbookMonitored || book.InAudiobookLibrary {
		t.Fatalf("membership after add = %+v", book)
	}
	a.want(a.call("GET", "/api/v1/libraries", nil, &libs), http.StatusOK)
	for _, l := range libs {
		if l.Active {
			t.Fatalf("content alone must not activate a library: %+v", libs)
		}
	}
	var home []library.HomeSection
	a.want(a.call("GET", "/api/v1/home", nil, &home), http.StatusOK)
	if len(home) != 0 {
		t.Fatalf("home must be empty before any library exists, got %+v", home)
	}

	// Creating the Ebooks library (adding a root folder) activates it.
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "path": t.TempDir()}, nil), http.StatusCreated)
	a.want(a.call("GET", "/api/v1/libraries", nil, &libs), http.StatusOK)
	for _, l := range libs {
		if (l.MediaType == "ebook") != l.Active {
			t.Fatalf("library activity wrong: %+v", libs)
		}
	}

	// Home: one section (ebook), the book in recently-added and wanted.
	a.want(a.call("GET", "/api/v1/home", nil, &home), http.StatusOK)
	if len(home) != 1 || home[0].MediaType != "ebook" ||
		len(home[0].RecentlyAdded) != 1 || len(home[0].Wanted) != 1 {
		t.Fatalf("home = %+v", home)
	}

	// Cross-add to Audiobooks with monitoring: the scoped author list sees it.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", book.ID),
		map[string]any{"library": "audiobook", "member": true, "monitored": true}, &book), http.StatusOK)
	if !book.InAudiobookLibrary || !book.AudiobookMonitored {
		t.Fatalf("cross-add failed: %+v", book)
	}
	var authors []library.Author
	a.want(a.call("GET", "/api/v1/author?library=audiobook", nil, &authors), http.StatusOK)
	if len(authors) != 1 {
		t.Fatalf("audiobook authors = %+v", authors)
	}

	authorID := authors[0].ID

	// Remove the book from Ebooks: the book leaves the grid, but the AUTHOR
	// stays in the ebook library (author-level membership) with 0 visible
	// books — the Missing section is the author's remaining content.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", book.ID),
		map[string]any{"library": "ebook", "member": false}, &book), http.StatusOK)
	if book.InEbookLibrary {
		t.Fatalf("book still in ebook library after removal: %+v", book)
	}
	a.want(a.call("GET", "/api/v1/author?library=ebook", nil, &authors), http.StatusOK)
	if len(authors) != 1 || authors[0].BookCount != 0 {
		t.Fatalf("ebook authors after book removal = %+v, want the author with 0 books", authors)
	}

	// Removing the AUTHOR from Ebooks clears that library only.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/author/%d/library", authorID),
		map[string]any{"library": "ebook", "member": false}, nil), http.StatusOK)
	a.want(a.call("GET", "/api/v1/author?library=ebook", nil, &authors), http.StatusOK)
	if len(authors) != 0 {
		t.Fatalf("ebook authors after author removal = %+v", authors)
	}
	a.want(a.call("GET", "/api/v1/author?library=audiobook", nil, &authors), http.StatusOK)
	if len(authors) != 1 {
		t.Fatalf("audiobook authors must be untouched, got %+v", authors)
	}
	// The book's audiobook membership survived the ebook-side removal.
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", book.ID), nil, &book), http.StatusOK)
	if !book.InAudiobookLibrary || !book.AudiobookMonitored || book.InEbookLibrary {
		t.Fatalf("book membership after ebook author removal = %+v", book)
	}

	// Removing from the last library deletes the author (and books) outright.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/author/%d/library", authorID),
		map[string]any{"library": "audiobook", "member": false}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d", authorID), nil, nil), http.StatusNotFound)
}

func TestRefreshEndpoints(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)

	var refreshed library.Author
	a.want(a.call("POST", fmt.Sprintf("/api/v1/author/%d/refresh", author.ID), nil, &refreshed), http.StatusOK)
	if refreshed.ID != author.ID || len(refreshed.Books) == 0 {
		t.Errorf("refreshed author = %+v", refreshed)
	}

	var refreshedBook library.Book
	a.want(a.call("POST", fmt.Sprintf("/api/v1/book/%d/refresh", book.ID), nil, &refreshedBook), http.StatusOK)
	if refreshedBook.ID != book.ID || len(refreshedBook.Editions) != 3 {
		t.Errorf("refreshed book = %+v", refreshedBook)
	}

	a.want(a.call("POST", "/api/v1/author/9999/refresh", nil, nil), http.StatusNotFound)
	a.want(a.call("POST", "/api/v1/book/9999/refresh", nil, nil), http.StatusNotFound)
}

func TestAddAuthorFlow(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	if author.ID == 0 || author.Name != "Terry Pratchett" || !author.Monitored {
		t.Fatalf("created author = %+v", author)
	}
	if len(author.Books) != 2 {
		t.Fatalf("author created with %d books, want 2", len(author.Books))
	}
	for _, b := range author.Books {
		if !b.Monitored {
			t.Errorf("book %q not monitored after author add", b.Title)
		}
	}

	// Unknown author at the provider → 404.
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "999"}, nil), http.StatusNotFound)

	// Adding again is an idempotent refresh, not a duplicate.
	var again library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &again), http.StatusCreated)
	if again.ID != author.ID {
		t.Errorf("re-add created a new author: id %d != %d", again.ID, author.ID)
	}

	var authors []library.Author
	a.want(a.call("GET", "/api/v1/author", nil, &authors), http.StatusOK)
	if len(authors) != 1 {
		t.Fatalf("listed %d authors, want 1", len(authors))
	}

	var monResp map[string]any
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/author/%d/monitor", author.ID),
		map[string]bool{"monitored": false}, &monResp), http.StatusOK)
	var detail library.Author
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d", author.ID), nil, &detail), http.StatusOK)
	if detail.Monitored {
		t.Error("author still monitored after unmonitor")
	}

	// Delete cascades to books.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/author/%d", author.ID), nil, nil), http.StatusNoContent)
	var books []library.Book
	a.want(a.call("GET", "/api/v1/book", nil, &books), http.StatusOK)
	if len(books) != 0 {
		t.Errorf("%d books survived author delete", len(books))
	}
}

func TestAddBookFlow(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var book library.Book
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "1"}, &book), http.StatusCreated)
	if book.ID == 0 || book.Title != "The Colour of Magic" || !book.Monitored {
		t.Fatalf("created book = %+v", book)
	}

	// Author was created as an unmonitored stub, not a full bibliography add.
	var authors []library.Author
	a.want(a.call("GET", "/api/v1/author", nil, &authors), http.StatusOK)
	if len(authors) != 1 || authors[0].Monitored {
		t.Fatalf("authors after book add = %+v", authors)
	}
	var allBooks []library.Book
	a.want(a.call("GET", "/api/v1/book", nil, &allBooks), http.StatusOK)
	if len(allBooks) != 1 {
		t.Fatalf("%d books in library, want just the added one", len(allBooks))
	}

	// Editions landed (edition monitoring is retired — per-format library
	// membership decides what gets acquired; editions are reference info).
	if len(book.Editions) != 3 {
		t.Fatalf("book has %d editions, want 3", len(book.Editions))
	}

	// Series link persisted.
	if len(book.Series) != 1 || book.Series[0].Title != "Discworld" || book.Series[0].Position != 1 {
		t.Errorf("book series = %+v", book.Series)
	}

	var detail library.Book

	// Unmonitor the book itself.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/monitor", book.ID),
		map[string]bool{"monitored": false}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", book.ID), nil, &detail), http.StatusOK)
	if detail.Monitored {
		t.Error("book still monitored after unmonitor")
	}

	// Unknown book at the provider → 404.
	a.want(a.call("POST", "/api/v1/book", map[string]string{"foreignBookId": "999"}, nil), http.StatusNotFound)

	// Delete the book; the author stub stays.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/book/%d", book.ID), nil, nil), http.StatusNoContent)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", book.ID), nil, nil), http.StatusNotFound)
	a.want(a.call("GET", "/api/v1/author", nil, &authors), http.StatusOK)
	if len(authors) != 1 {
		t.Errorf("author stub deleted with book")
	}
}
