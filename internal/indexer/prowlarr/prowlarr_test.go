package prowlarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// newSingleIndexerSearcher wires up a searcher whose Prowlarr instance
// reports exactly one enabled sub-indexer (id subID), with searchHandler
// serving every /api/v1/search request — the shape most Search tests only
// care about, without each one having to hand-roll the /api/v1/indexer
// list response too.
func newSingleIndexerSearcher(t *testing.T, subID int, searchHandler http.HandlerFunc) indexer.Searcher {
	t.Helper()
	return newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]subIndexer{{ID: subID, Name: "SomeIndexer", Enable: true}})
			return
		}
		searchHandler(w, r)
	})
}

func TestSearchMapsResults(t *testing.T) {
	s := newSingleIndexerSearcher(t, 7, func(w http.ResponseWriter, r *http.Request) {
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
		if got := r.URL.Query().Get("indexerIds"); got != "7" {
			t.Errorf("indexerIds = %q, want 7 (Search should scope to the one sub-indexer)", got)
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

	var torrent, usenet indexer.Release
	for _, r := range got {
		if r.Protocol == indexer.ProtocolTorrent {
			torrent = r
		} else {
			usenet = r
		}
	}
	if torrent.Protocol != indexer.ProtocolTorrent || torrent.Seeders != 42 || torrent.Peers != 3 {
		t.Errorf("torrent release = %+v", torrent)
	}
	if !strings.Contains(torrent.Indexer, "SomeTracker") {
		t.Errorf("indexer name should name the underlying tracker: %q", torrent.Indexer)
	}
	if torrent.DownloadURL != "https://prowlarr.example/download/abc" {
		t.Errorf("downloadURL = %q", torrent.DownloadURL)
	}
	if usenet.Protocol != indexer.ProtocolUsenet || usenet.Seeders != -1 || usenet.Peers != -1 {
		t.Errorf("usenet release = %+v", usenet)
	}
}

// TestSearchPrefersMagnetURL: when Prowlarr supplies a magnetUrl directly,
// it's used as-is instead of the (still-fetchable) downloadUrl — no HTTP
// round trip needed to resolve it.
func TestSearchPrefersMagnetURL(t *testing.T) {
	s := newSingleIndexerSearcher(t, 1, func(w http.ResponseWriter, r *http.Request) {
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

// TestSearchFansOutToEachEnabledSubIndexer proves the core of the redesign:
// every enabled sub-indexer gets its own scoped /api/v1/search?indexerIds=
// call, a disabled one is skipped entirely, and results from all of them
// merge into one slice.
func TestSearchFansOutToEachEnabledSubIndexer(t *testing.T) {
	var queried int32
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			_ = json.NewEncoder(w).Encode([]subIndexer{
				{ID: 1, Name: "Fast One", Enable: true},
				{ID: 2, Name: "Fast Two", Enable: true},
				{ID: 3, Name: "Disabled One", Enable: false},
			})
			return
		}
		id := r.URL.Query().Get("indexerIds")
		if id == "3" {
			t.Error("disabled sub-indexer 3 must never be queried")
		}
		atomic.AddInt32(&queried, 1)
		_ = json.NewEncoder(w).Encode([]release{{GUID: "g" + id, Title: "Release " + id}})
	})

	got, err := s.Search(context.Background(), "q", "music")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if queried != 2 {
		t.Errorf("queried %d sub-indexers, want exactly 2 (the enabled ones)", queried)
	}
	if len(got) != 2 {
		t.Fatalf("got %d releases, want 2 (one per enabled sub-indexer): %+v", len(got), got)
	}
}

// TestSearchSlowSubIndexerDoesNotBlockOthers is the regression test for the
// actual bug this feature fixes: one sub-indexer hanging past
// perSubIndexerTimeout must not delay the fast ones' results, and Search
// overall must return in roughly the timeout window, not the slow
// handler's real (much longer) delay.
func TestSearchSlowSubIndexerDoesNotBlockOthers(t *testing.T) {
	old := perSubIndexerTimeout
	perSubIndexerTimeout = 100 * time.Millisecond
	t.Cleanup(func() { perSubIndexerTimeout = old })

	// blockForever must be closed before httptest.Server.Close() runs (it
	// blocks until every in-flight handler returns) — t.Cleanup runs LIFO,
	// so this registration has to come *after* newSearcher's own internal
	// srv.Close() cleanup, not before it.
	blockForever := make(chan struct{})
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			_ = json.NewEncoder(w).Encode([]subIndexer{
				{ID: 1, Name: "Slow", Enable: true},
				{ID: 2, Name: "Fast", Enable: true},
			})
			return
		}
		if r.URL.Query().Get("indexerIds") == "1" {
			<-blockForever // never responds within the test's lifetime
			return
		}
		_ = json.NewEncoder(w).Encode([]release{{GUID: "fast", Title: "Fast Release"}})
	})
	t.Cleanup(func() { close(blockForever) })

	start := time.Now()
	got, err := s.Search(context.Background(), "q", "music")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Fast Release" {
		t.Fatalf("got = %+v, want just the fast sub-indexer's result", got)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Search took %v, want roughly perSubIndexerTimeout (100ms), not the slow handler's real delay", elapsed)
	}
}

// TestSearchFallsBackToAggregateWhenIndexerListFails proves the change is
// never worse than before it existed: if Prowlarr's own indexer list can't
// be fetched, Search degrades to the old single aggregate call (no
// indexerIds param) instead of returning nothing.
func TestSearchFallsBackToAggregateWhenIndexerListFails(t *testing.T) {
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/indexer" {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/api/v1/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("indexerIds"); got != "" {
			t.Errorf("indexerIds = %q, want empty (the aggregate fallback scopes to nothing)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]release{{GUID: "abc", Title: "Fallback Release"}})
	})

	got, err := s.Search(context.Background(), "q", "music")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Fallback Release" {
		t.Fatalf("got = %+v, want the aggregate fallback's result", got)
	}
}

// TestSearchSkipsRestingSubIndexer proves the backoff half of the fix:
// after enough consecutive failures against one sub-indexer, Search stops
// even attempting it until its rest window elapses.
func TestSearchSkipsRestingSubIndexer(t *testing.T) {
	now := time.Now()
	subIndexerBackoff = &subIndexerBackoffTracker{
		state: map[string]*subIndexerBackoffState{},
		now:   func() time.Time { return now },
	}
	t.Cleanup(func() {
		subIndexerBackoff = &subIndexerBackoffTracker{state: map[string]*subIndexerBackoffState{}, now: time.Now}
	})

	var badQueried, goodQueried int32
	s := newSearcher(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			_ = json.NewEncoder(w).Encode([]subIndexer{
				{ID: 1, Name: "Bad", Enable: true},
				{ID: 2, Name: "Good", Enable: true},
			})
			return
		}
		switch r.URL.Query().Get("indexerIds") {
		case "1":
			atomic.AddInt32(&badQueried, 1)
			http.Error(w, "boom", http.StatusInternalServerError)
		case "2":
			atomic.AddInt32(&goodQueried, 1)
			_ = json.NewEncoder(w).Encode([]release{{GUID: "g", Title: "Good Release"}})
		}
	})

	// restAfter (3) consecutive failures against sub-indexer 1.
	for i := 0; i < 3; i++ {
		if _, err := s.Search(context.Background(), "q", "music"); err != nil {
			t.Fatalf("Search #%d: %v", i, err)
		}
	}
	if badQueried != 3 {
		t.Fatalf("badQueried = %d, want 3 (tolerated before resting)", badQueried)
	}

	// Now resting: a follow-up call must not query it again.
	badQueried = 0
	if _, err := s.Search(context.Background(), "q", "music"); err != nil {
		t.Fatalf("Search after resting: %v", err)
	}
	if badQueried != 0 {
		t.Errorf("badQueried = %d, want 0 — sub-indexer 1 should be resting", badQueried)
	}
	if goodQueried == 0 {
		t.Error("the healthy sub-indexer should still be queried while the bad one rests")
	}

	// Advance the fake clock past the rest window; it should be tried again.
	now = now.Add(subIndexerBackoffBase + time.Second)
	badQueried = 0
	if _, err := s.Search(context.Background(), "q", "music"); err != nil {
		t.Fatalf("Search after rest window: %v", err)
	}
	if badQueried == 0 {
		t.Error("sub-indexer 1 should be tried again once its rest window elapses")
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
