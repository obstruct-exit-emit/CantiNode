package comicvine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/metadata"
)

const volumeJSON = `{
	"id": 18166, "name": "The Walking Dead",
	"description": "<p>Zombies.</p>", "start_year": "2003",
	"count_of_issues": 193,
	"image": {"medium_url": "https://cv/twd.jpg"},
	"person_credits": [{"name": "Robert Kirkman", "role": "writer"}],
	"issues": [
		{"id": 111, "issue_number": "1", "name": "Days Gone Bye"},
		{"id": 112, "issue_number": "2", "name": ""},
		{"id": 113, "issue_number": "1.AU", "name": "Weird Special"}
	]
}`

func mockComicVine(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "LibriNode" {
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("api_key") != "cv-key" {
			w.Write([]byte(`{"status_code": 100, "error": "Invalid API Key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/search"):
			w.Write([]byte(`{"status_code": 1, "error": "OK", "results": [` + volumeJSON + `]}`))
		case strings.HasPrefix(r.URL.Path, "/volume/4050-18166"):
			w.Write([]byte(`{"status_code": 1, "error": "OK", "results": ` + volumeJSON + `}`))
		default:
			w.Write([]byte(`{"status_code": 101, "error": "Object Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return New("cv-key", WithEndpoint(srv.URL))
}

func TestSearchSeries(t *testing.T) {
	c := mockComicVine(t)
	results, err := c.SearchSeries(context.Background(), "walking dead")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	r := results[0]
	if r.ForeignID != "18166" || r.Title != "The Walking Dead" || r.AuthorName != "Robert Kirkman" {
		t.Errorf("result = %+v", r)
	}
	if r.Year != 2003 || r.IssueCount != 193 || r.Description != "Zombies." {
		t.Errorf("fields = %+v", r)
	}
}

func TestGetSeries(t *testing.T) {
	c := mockComicVine(t)
	s, err := c.GetSeries(context.Background(), "18166")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	// Oddly-numbered specials are skipped.
	if len(s.Issues) != 2 {
		t.Fatalf("issues = %+v", s.Issues)
	}
	if s.Issues[0].Number != 1 || s.Issues[0].Title != "Days Gone Bye" {
		t.Errorf("issue 1 = %+v", s.Issues[0])
	}

	if _, err := c.GetSeries(context.Background(), "999"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("missing: err = %v, want ErrNotFound", err)
	}
}

// TestValidateUnreachableVsRejected: a transport failure or 5xx/429 response
// wraps metadata.ErrUnreachable (the key may be fine — ComicVine just isn't
// answering); an invalid-key response (status_code 100) does not, since
// that's a real "go fix your key" problem.
func TestValidateUnreachableVsRejected(t *testing.T) {
	down := New("cv-key", WithEndpoint("http://127.0.0.1:1"))
	if err := down.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("transport failure: err = %v, want ErrUnreachable", err)
	}

	rateLimited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status_code": 102, "error": "Rate limit exceeded"}`))
	}))
	t.Cleanup(rateLimited.Close)
	rl := New("cv-key", WithEndpoint(rateLimited.URL))
	if err := rl.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("rate-limited response: err = %v, want ErrUnreachable", err)
	}

	badKey := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status_code": 100, "error": "Invalid API Key"}`))
	}))
	t.Cleanup(badKey.Close)
	bk := New("wrong-key", WithEndpoint(badKey.URL))
	if err := bk.Validate(context.Background()); err == nil || errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("invalid-key response: err = %v, want a non-ErrUnreachable error", err)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status_code": 1, "error": "OK", "results": []}`))
	}))
	t.Cleanup(ok.Close)
	good := New("cv-key", WithEndpoint(ok.URL))
	if err := good.Validate(context.Background()); err != nil {
		t.Errorf("Validate against a healthy mock: %v, want nil", err)
	}
}

// TestValidateNeverLeaksAPIKey: ComicVine's api_key rides in every request's
// query string — a connection failure must not carry it into the returned
// error, since that string can reach the health banner and, for background
// checks, the log output.
func TestValidateNeverLeaksAPIKey(t *testing.T) {
	secret := "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	c := New(secret, WithEndpoint("http://127.0.0.1:1"))
	err := c.Validate(context.Background())
	if err == nil {
		t.Fatal("expected Validate to fail against an unreachable host")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("API key leaked into error: %q", err)
	}
}

func TestBadKey(t *testing.T) {
	c := mockComicVine(t)
	c.apiKey = "wrong"
	if _, err := c.SearchSeries(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Errorf("err = %v, want invalid key error", err)
	}
}
