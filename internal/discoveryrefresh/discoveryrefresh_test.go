package discoveryrefresh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// newTestDeps wires a Service against an in-memory database and a fake
// MusicBrainz server that answers both LookupArtist and
// BrowseArtistReleaseGroups with an empty-but-successful discography --
// enough for PollOnce's own bookkeeping (Checked/Refreshed) without
// needing real release-group fixtures for tests that don't care about
// the discography content itself.
func newTestDeps(t *testing.T, handler http.HandlerFunc) (*Service, *musiclibrary.Store) {
	t.Helper()
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/release-group/":
				_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-mbid", "name": "Test Artist"})
			}
		}
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := musiclibrary.NewStore(db)
	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", srv.URL)
	return New(store, discography.New(mb, store)), store
}

func TestPollOnceSkipsUnmonitoredArtist(t *testing.T) {
	s, store := newTestDeps(t, nil)
	if _, err := store.GetOrCreateArtist("a-mbid", "Unmonitored", "Unmonitored"); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 0 || result.Refreshed != 0 {
		t.Errorf("result = %+v, want nothing touched for an unmonitored artist", result)
	}
}

func TestPollOnceRefreshesMonitoredArtist(t *testing.T) {
	s, store := newTestDeps(t, nil)
	artist, err := store.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetArtistMonitored(artist.ID, true); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 1 || result.Refreshed != 1 {
		t.Fatalf("result = %+v, want 1 checked and refreshed", result)
	}

	refreshed, err := store.GetArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.LastSyncedAt == nil {
		t.Error("LastSyncedAt should be set after a successful refresh")
	}
}

// TestPollOneArtistFailureDoesNotStopSweep: one monitored artist whose
// MusicBrainz lookup 404s must not prevent the other monitored artist's
// refresh from running -- the same non-aborting pattern
// internal/autosearch.Service.PollOnce uses for a single album's search
// failure.
func TestPollOnceOneArtistFailureDoesNotStopSweep(t *testing.T) {
	s, store := newTestDeps(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/artist/bad-mbid":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/release-group/":
			_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "good-mbid", "name": "Good Artist"})
		}
	})

	bad, err := store.GetOrCreateArtist("bad-mbid", "Bad Artist", "Bad Artist")
	if err != nil {
		t.Fatal(err)
	}
	good, err := store.GetOrCreateArtist("good-mbid", "Good Artist", "Good Artist")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{bad.ID, good.ID} {
		if err := store.SetArtistMonitored(id, true); err != nil {
			t.Fatal(err)
		}
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 2 {
		t.Errorf("Checked = %d, want 2 (both attempted)", result.Checked)
	}
	if result.Refreshed != 1 {
		t.Errorf("Refreshed = %d, want 1 (only the good one)", result.Refreshed)
	}

	refreshedGood, err := store.GetArtist(good.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshedGood.LastSyncedAt == nil {
		t.Error("the good artist should still have been refreshed despite the bad one's failure")
	}
}

func TestRunPeriodicRepeatsUntilCanceled(t *testing.T) {
	s, _ := newTestDeps(t, nil)

	var calls int32
	next := func(now time.Time) time.Time {
		atomic.AddInt32(&calls, 1)
		return now.Add(2 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.RunPeriodic(ctx, next)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("next called %d time(s) in 50ms of 2ms cycles, want at least 2 — RunPeriodic should keep looping until ctx is done", got)
	}
}

// TestRunPeriodicNilScheduleFallsBackAndStopsOnCancel: a nil schedule
// function must not panic (falls back to a plain PollInterval ticker),
// and a canceled context must stop the wait immediately rather than
// sitting through the full 24h fallback.
func TestRunPeriodicNilScheduleFallsBackAndStopsOnCancel(t *testing.T) {
	s, _ := newTestDeps(t, nil)

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		s.RunPeriodic(ctx, nil)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic did not return promptly after ctx was canceled")
	}
}
