package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "test-token")
}

func TestMusicSectionsFiltersToArtistType(t *testing.T) {
	var gotToken string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Plex-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"size":3,"Directory":[
			{"key":"1","title":"Movies","type":"movie"},
			{"key":"2","title":"Music","type":"artist"},
			{"key":"3","title":"Music 2","type":"artist"}
		]}}`))
	})

	got, err := c.MusicSections(t.Context())
	if err != nil {
		t.Fatalf("MusicSections: %v", err)
	}
	if len(got) != 2 || got[0].Key != "2" || got[1].Key != "3" {
		t.Errorf("got = %+v, want only the two artist-type sections", got)
	}
	if gotToken != "test-token" {
		t.Errorf("X-Plex-Token = %q, want test-token", gotToken)
	}
}

func TestRefreshPathSendsPathAndSectionKey(t *testing.T) {
	var gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	if err := c.RefreshPath(t.Context(), "2", "/music/Boards of Canada"); err != nil {
		t.Fatalf("RefreshPath: %v", err)
	}
	if gotPath != "/library/sections/2/refresh" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "path=%2Fmusic%2FBoards+of+Canada" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestRefreshPathRequiresSectionKey(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not make a request with no section key")
	})
	if err := c.RefreshPath(t.Context(), "", "/music"); err == nil {
		t.Fatal("expected an error for a missing section key")
	}
}

func TestGetSurfacesInvalidToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, err := c.MusicSections(t.Context()); err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}
