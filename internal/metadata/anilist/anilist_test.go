package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/librinode/librinode/internal/metadata"
)

const mediaJSON = `{
	"id": 30002, "title": {"english": "Berserk", "romaji": "Berserk"},
	"description": "<p>Guts, a former mercenary.</p>",
	"volumes": 41, "startDate": {"year": 1989},
	"coverImage": {"large": "https://img.anilist/berserk.jpg"},
	"staff": {"edges": [
		{"role": "Story & Art", "node": {"name": {"full": "Kentarou Miura"}}},
		{"role": "Assistant", "node": {"name": {"full": "Someone Else"}}}
	]}
}`

func mockAniList(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, ok := req.Variables["search"]; ok {
			w.Write([]byte(`{"data": {"Page": {"media": [` + mediaJSON + `]}}}`))
			return
		}
		if id, ok := req.Variables["id"].(float64); ok && int(id) == 30002 {
			w.Write([]byte(`{"data": {"Media": ` + mediaJSON + `}}`))
			return
		}
		w.Write([]byte(`{"data": null, "errors": [{"message": "Not Found."}]}`))
	}))
	t.Cleanup(srv.Close)
	return New(WithEndpoint(srv.URL))
}

// TestValidateUnreachable: AniList needs no key, so Validate only ever
// reports connectivity — confirm a dead endpoint and a 5xx both wrap
// metadata.ErrUnreachable (used by the health check to phrase the banner as
// "unreachable" rather than a token problem that doesn't exist here).
func TestValidateUnreachable(t *testing.T) {
	down := New(WithEndpoint("http://127.0.0.1:1"))
	if err := down.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("transport failure: err = %v, want ErrUnreachable", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	unavailable := New(WithEndpoint(srv.URL))
	if err := unavailable.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("503 response: err = %v, want ErrUnreachable", err)
	}

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": {"__typename": "Query"}}`))
	}))
	t.Cleanup(okSrv.Close)
	ok := New(WithEndpoint(okSrv.URL))
	if err := ok.Validate(context.Background()); err != nil {
		t.Errorf("Validate against a healthy mock: %v, want nil", err)
	}
}

func TestSearchSeries(t *testing.T) {
	c := mockAniList(t)
	results, err := c.SearchSeries(context.Background(), "berserk")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	r := results[0]
	if r.ForeignID != "30002" || r.Title != "Berserk" || r.AuthorName != "Kentarou Miura" {
		t.Errorf("result = %+v", r)
	}
	if r.IssueCount != 41 || r.Year != 1989 {
		t.Errorf("counts = %+v", r)
	}
	if r.Description != "Guts, a former mercenary." {
		t.Errorf("description not de-HTML'd: %q", r.Description)
	}
}

// TestSearchSeriesAdultFilter: adult-flagged series stay out of search
// results unless the global include-adult preference is on; the language
// preference also picks romaji titles for non-English users.
func TestSearchSeriesAdultFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data": {"Page": {"media": [
			{"id": 1, "isAdult": false, "title": {"english": "Attack on Titan", "romaji": "Shingeki no Kyojin"}, "volumes": 34},
			{"id": 2, "isAdult": true, "title": {"english": "Adult Thing", "romaji": "Adult Thing"}, "volumes": 3}
		]}}}`))
	}))
	t.Cleanup(srv.Close)

	c := New(WithEndpoint(srv.URL)) // defaults: adult hidden, English titles
	results, err := c.SearchSeries(context.Background(), "titan")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 1 || results[0].ForeignID != "1" {
		t.Fatalf("adult result leaked through the default filter: %+v", results)
	}
	if results[0].Title != "Attack on Titan" {
		t.Errorf("default title = %q, want the English one", results[0].Title)
	}

	c = New(WithEndpoint(srv.URL))
	c.includeAdult = true
	c.preferRomaji = true // a non-English language preference
	if results, err = c.SearchSeries(context.Background(), "titan"); err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("include-adult should surface both results: %+v", results)
	}
	if results[0].Title != "Shingeki no Kyojin" {
		t.Errorf("romaji preference ignored: %q", results[0].Title)
	}
}

func TestGetSeries(t *testing.T) {
	c := mockAniList(t)
	s, err := c.GetSeries(context.Background(), "30002")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(s.Issues) != 41 {
		t.Fatalf("issues = %d, want 41 synthesized volumes", len(s.Issues))
	}
	if s.Issues[4].Number != 5 || s.Issues[4].Title != "Vol. 5" {
		t.Errorf("issue 5 = %+v", s.Issues[4])
	}

	if _, err := c.GetSeries(context.Background(), "999"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("missing series: err = %v, want ErrNotFound", err)
	}
}
