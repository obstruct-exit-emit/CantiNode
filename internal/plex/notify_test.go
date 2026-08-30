package plex

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/config"
)

func TestDistinctDirs(t *testing.T) {
	got := distinctDirs([]string{
		"/music/Boards of Canada/Geogaddi/01 - Alpha.flac",
		"/music/Boards of Canada/Geogaddi/02 - Beta.flac",
		"/music/Boards of Canada/Music Has the Right/01 - Gamma.flac",
	})
	want := []string{
		"/music/Boards of Canada/Geogaddi",
		"/music/Boards of Canada/Music Has the Right",
	}
	if len(got) != len(want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNotifyPathsNoopWhenDisabledOrUnconfigured(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	cases := []config.PlexSettings{
		{Enabled: false, ServerURL: srv.URL, Token: "t", SectionKey: "1"},
		{Enabled: true, ServerURL: "", Token: "t", SectionKey: "1"},
		{Enabled: true, ServerURL: srv.URL, Token: "", SectionKey: "1"},
		{Enabled: true, ServerURL: srv.URL, Token: "t", SectionKey: ""},
	}
	for _, settings := range cases {
		NotifyPaths(settings, nil, []string{"/music/Artist/Album/track.flac"})
	}
	time.Sleep(50 * time.Millisecond) // NotifyPaths backgrounds its work; give it a chance to (wrongly) fire
	if called {
		t.Error("expected no Plex request for a disabled or incompletely-configured settings value")
	}
}

// TestNotifyPathsCallsRefreshPerDistinctDirTranslated is the regression
// test for the whole feature's actual point: a batch of changed files
// under two different album folders triggers exactly one refresh call per
// folder, each translated through the configured path mapping (CantiNode's
// own path -> the path Plex itself sees for the same file).
func TestNotifyPathsCallsRefreshPerDistinctDirTranslated(t *testing.T) {
	var mu sync.Mutex
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPaths = append(gotPaths, r.URL.Query().Get("path"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	settings := config.PlexSettings{
		Enabled:    true,
		ServerURL:  srv.URL,
		Token:      "t",
		SectionKey: "5",
		PathMappings: []config.PathMapping{
			{RemotePrefix: "/mnt/music", LocalPrefix: "/data/music"},
		},
	}
	NotifyPaths(settings, nil, []string{
		"/mnt/music/Boards of Canada/Geogaddi/01 - Alpha.flac",
		"/mnt/music/Boards of Canada/Geogaddi/02 - Beta.flac",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(gotPaths)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotPaths) != 1 {
		t.Fatalf("gotPaths = %v, want exactly 1 call (one distinct directory)", gotPaths)
	}
	if gotPaths[0] != "/data/music/Boards of Canada/Geogaddi" {
		t.Errorf("path = %q, want the translated album directory", gotPaths[0])
	}
}
