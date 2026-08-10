package prowlarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/indexer"
)

func newSearcher(t *testing.T, handler http.HandlerFunc) indexer.Searcher {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	def := Def()
	ind := &indexer.Indexer{Name: "My Prowlarr", Type: "prowlarr", BaseURL: srv.URL, APIKey: "secret-key"}
	return def.New(ind, srv.Client())
}

func TestSearchMapsResults(t *testing.T) {
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "secret-key" {
			t.Errorf("X-Api-Key = %q", got)
		}
		if got := r.URL.Query().Get("query"); got != "Boards of Canada Geogaddi" {
			t.Errorf("query = %q", got)
		}
		if cats := r.URL.Query()["categories"]; len(cats) != 2 || cats[0] != "3010" || cats[1] != "3040" {
			t.Errorf("categories = %v, want default [3010 3040]", cats)
		}
		seeders, leechers := 42, 3
		results := []release{
			{
				GUID: "abc", Title: "Boards of Canada - Geogaddi FLAC", Size: 400 << 20,
				Indexer: "SomeTracker", DownloadURL: "https://prowlarr.example/download/abc",
				Protocol: protocol(indexer.ProtocolTorrent), Seeders: &seeders, Leechers: &leechers,
				Categories: []category{{ID: 3040}},
			},
			{
				GUID: "def", Title: "Boards of Canada - Geogaddi MP3", Size: 100 << 20,
				Indexer: "SomeUsenetIndexer", DownloadURL: "https://prowlarr.example/download/def",
				Protocol: protocol(indexer.ProtocolUsenet),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(results)
	})

	got, err := s.Search(context.Background(), "Boards of Canada Geogaddi", "music")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2: %+v", len(got), got)
	}

	torrent := got[0]
	if torrent.Protocol != indexer.ProtocolTorrent || torrent.Seeders != 42 || torrent.Peers != 3 {
		t.Errorf("torrent release = %+v", torrent)
	}
	if !strings.Contains(torrent.Indexer, "SomeTracker") {
		t.Errorf("indexer name should name the underlying tracker: %q", torrent.Indexer)
	}
	if torrent.DownloadURL != "https://prowlarr.example/download/abc" {
		t.Errorf("downloadURL = %q", torrent.DownloadURL)
	}

	usenet := got[1]
	if usenet.Protocol != indexer.ProtocolUsenet || usenet.Seeders != -1 || usenet.Peers != -1 {
		t.Errorf("usenet release = %+v", usenet)
	}
}

// TestSearchPrefersMagnetURL: when Prowlarr supplies a magnetUrl directly,
// it's used as-is instead of the (still-fetchable) downloadUrl — no HTTP
// round trip needed to resolve it.
func TestSearchPrefersMagnetURL(t *testing.T) {
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		results := []release{{
			GUID: "abc", Title: "A Release", DownloadURL: "https://prowlarr.example/download/abc",
			MagnetURL: "magnet:?xt=urn:btih:aaaa", Protocol: protocol(indexer.ProtocolTorrent),
		}}
		_ = json.NewEncoder(w).Encode(results)
	})
	got, err := s.Search(context.Background(), "q", "music")
	if err != nil || len(got) != 1 || got[0].DownloadURL != "magnet:?xt=urn:btih:aaaa" {
		t.Fatalf("Search = %+v, %v", got, err)
	}
}

func TestSearchNonMusicYieldsNothing(t *testing.T) {
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called for a media type Prowlarr's music search doesn't serve")
	})
	got, err := s.Search(context.Background(), "q", "ebook")
	if err != nil || got != nil {
		t.Errorf("Search(ebook) = %+v, %v; want nil, nil", got, err)
	}
}

func TestSearchRequiresBaseURL(t *testing.T) {
	def := Def()
	s := def.New(&indexer.Indexer{Name: "No URL", Type: "prowlarr", APIKey: "k"}, http.DefaultClient)
	if _, err := s.Search(context.Background(), "q", "music"); err == nil {
		t.Error("expected an error without a base URL")
	}
	if err := s.Test(context.Background()); err == nil {
		t.Error("expected Test to fail without a base URL")
	}
}

func TestTestHitsSystemStatus(t *testing.T) {
	hit := false
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/system/status" {
			hit = true
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("unexpected path %q", r.URL.Path)
	})
	if err := s.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if !hit {
		t.Error("Test should hit /api/v1/system/status")
	}
}

func TestTestFailsOnBadStatus(t *testing.T) {
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	})
	if err := s.Test(context.Background()); err == nil {
		t.Error("expected an error on HTTP 401")
	}
}

// TestProtocolUnmarshalsIntOrString covers the *arr-family enum ambiguity
// Prowlarr's own protocol field is observed to serialize either way.
func TestProtocolUnmarshalsIntOrString(t *testing.T) {
	cases := []struct {
		json string
		want protocol
	}{
		{`"torrent"`, protocol(indexer.ProtocolTorrent)},
		{`"usenet"`, protocol(indexer.ProtocolUsenet)},
		{`"Torrent"`, protocol(indexer.ProtocolTorrent)},
		{`2`, protocol(indexer.ProtocolTorrent)},
		{`1`, protocol(indexer.ProtocolUsenet)},
		{`0`, protocol("")},
		{`"unknown"`, protocol("")},
	}
	for _, c := range cases {
		var p protocol
		if err := json.Unmarshal([]byte(c.json), &p); err != nil {
			t.Errorf("Unmarshal(%s): %v", c.json, err)
			continue
		}
		if p != c.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", c.json, p, c.want)
		}
	}
}

func TestDefRegistersCorrectly(t *testing.T) {
	def := Def()
	if def.Name != "prowlarr" || !def.NeedsAPIKey || def.New == nil {
		t.Errorf("Def() = %+v", def)
	}
	if len(def.MediaTypes) != 1 || def.MediaTypes[0] != "music" {
		t.Errorf("MediaTypes = %v", def.MediaTypes)
	}
}
