package metadatabackfill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/audiodb"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// newTestDeps wires a Service against an in-memory database, a fake
// MusicBrainz server (LookupArtist + an empty-but-successful
// BrowseArtistReleaseGroups), and a fake TheAudioDB server that has
// nothing for any artist (a real, common, non-error outcome — see
// audiodb.Client.LookupArtistByMBID's own doc comment) — enough for
// PollOnce's own bookkeeping without needing real discography/bio
// fixtures for tests that don't care about the cached content itself.
func newTestDeps(t *testing.T, mbHandler http.HandlerFunc) (*Service, *musiclibrary.Store) {
	t.Helper()
	if mbHandler == nil {
		mbHandler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/release-group/":
				_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-mbid", "name": "Test Artist"})
			}
		}
	}
	mbSrv := httptest.NewServer(mbHandler)
	t.Cleanup(mbSrv.Close)

	audiodbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": nil})
	}))
	t.Cleanup(audiodbSrv.Close)

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := musiclibrary.NewStore(db)

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", mbSrv.URL)
	audiodbClient := audiodb.NewClientWithBaseURL("test-key", audiodbSrv.URL)
	return New(store, mb, audiodbClient, discography.New(mb, store)), store
}

func TestPollOnceSkipsArtistWithMetadataAlreadyFetched(t *testing.T) {
	s, store := newTestDeps(t, nil)
	artist, err := store.GetOrCreateArtist("a-mbid", "Already Fetched", "Already Fetched")
	if err != nil {
		t.Fatal(err)
	}
	// ListArtists only ever returns an artist that's monitored or owns a
	// file (see its own doc comment) — monitoring is the simplest way to
	// make a bare GetOrCreateArtist row visible to PollOnce for this test.
	if err := store.SetArtistMonitored(artist.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetArtistMetadata(artist.ID, "existing bio", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 0 || result.Cached != 0 {
		t.Errorf("result = %+v, want nothing touched for an artist that already has metadata", result)
	}
}

func TestPollOnceCachesArtistMissingMetadata(t *testing.T) {
	s, store := newTestDeps(t, nil)
	artist, err := store.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetArtistMonitored(artist.ID, true); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 1 || result.Cached != 1 {
		t.Fatalf("result = %+v, want 1 checked and cached", result)
	}

	refreshed, err := store.GetArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.MetadataFetchedAt == nil {
		t.Error("MetadataFetchedAt should be set after a successful poll")
	}
}

// TestCacheReleaseGroupVersionsSkipsAlreadyCached is the regression test
// for a real hammering bug: CacheReleaseGroupVersions used to call
// BrowseReleaseGroupReleases unconditionally, even for a release group
// whose version list was already fully cached. The MusicBrainz fake here
// fails the test outright if that endpoint is ever hit, proving the skip
// actually happens — a live consequence: CacheFullArtistMetadata's own
// retry path (see its own doc comment: a transient TheAudioDB failure
// leaves MetadataFetchedAt unset specifically so a later sweep retries)
// used to re-fetch an artist's ENTIRE discography's worth of version lists
// from MusicBrainz on every single retry, forever, until TheAudioDB
// happened to succeed once.
func TestCacheReleaseGroupVersionsSkipsAlreadyCached(t *testing.T) {
	s, store := newTestDeps(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release/" {
			t.Fatal("BrowseReleaseGroupReleases should never be called for an already-cached release group")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-mbid", "name": "Test Artist"})
	})

	seeded := []musiclibrary.ReleaseGroupVersion{
		{ReleaseMBID: "release-1", Title: "Geogaddi", ReleaseDate: "2002-02-04", IsRepresentative: true},
	}
	if err := store.ReplaceReleaseGroupVersions("rg-mbid", seeded); err != nil {
		t.Fatal(err)
	}

	got, err := s.CacheReleaseGroupVersions(context.Background(), "rg-mbid")
	if err != nil {
		t.Fatalf("CacheReleaseGroupVersions: %v", err)
	}
	if len(got) != 1 || got[0].ReleaseMBID != "release-1" {
		t.Errorf("got = %+v, want the already-cached version returned untouched", got)
	}
}

// TestCacheDiscographyVersionsDoesNotRefetchOnASecondPass reproduces the
// live scenario end to end: two release groups get cached on a first pass
// (a fresh artist's initial discography sync), then CacheDiscographyVersions
// runs again for the exact same groups (standing in for
// CacheFullArtistMetadata's retry-on-TheAudioDB-failure path) — the second
// pass must not issue a single additional MusicBrainz request.
func TestCacheDiscographyVersionsDoesNotRefetchOnASecondPass(t *testing.T) {
	var browseRequests int
	s, _ := newTestDeps(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/release/":
			browseRequests++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"release-count": 1,
				"releases":      []map[string]any{{"id": "release-" + r.URL.Query().Get("release-group"), "title": "Album", "media": []any{}}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-mbid", "name": "Test Artist"})
		}
	})

	groups := []musiclibrary.ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Album One"},
		{ReleaseGroupMBID: "rg-2", Title: "Album Two"},
	}

	s.CacheDiscographyVersions(context.Background(), groups)
	if browseRequests != 2 {
		t.Fatalf("first pass: browseRequests = %d, want 2 (one per release group)", browseRequests)
	}

	s.CacheDiscographyVersions(context.Background(), groups)
	if browseRequests != 2 {
		t.Errorf("second pass: browseRequests = %d, want still 2 — already-cached release groups must not be re-fetched", browseRequests)
	}
}

// TestPollOnceOneArtistFailureDoesNotStopSweep: one artist whose
// MusicBrainz lookup 404s must not prevent the other artist's metadata
// from being cached — the same non-aborting pattern
// internal/discoveryrefresh.Service.PollOnce uses for a single artist's
// refresh failure.
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
	if result.Cached != 1 {
		t.Errorf("Cached = %d, want 1 (only the good one)", result.Cached)
	}

	refreshedGood, err := store.GetArtist(good.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshedGood.MetadataFetchedAt == nil {
		t.Error("the good artist should still have been cached despite the bad one's failure")
	}
}

// TestRunPeriodicSweepsImmediatelyThenAgainOnTick proves RunPeriodic
// actually loops on its own interval rather than sweeping once and
// blocking forever: an artist added after the immediate first sweep
// (which finds nothing to do) still gets caught by a later tick.
func TestRunPeriodicSweepsImmediatelyThenAgainOnTick(t *testing.T) {
	s, store := newTestDeps(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunPeriodic(ctx, 5*time.Millisecond)

	artist, err := store.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetArtistMonitored(artist.ID, true); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		refreshed, err := store.GetArtist(artist.ID)
		if err != nil {
			t.Fatal(err)
		}
		if refreshed.MetadataFetchedAt != nil {
			return // caught by a later tick, as expected
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("artist added after RunPeriodic started was never picked up by a later tick")
}

// TestRunPeriodicStopsPromptlyOnCancel proves canceling ctx stops the wait
// immediately rather than sitting through the full PollInterval fallback.
func TestRunPeriodicStopsPromptlyOnCancel(t *testing.T) {
	s, _ := newTestDeps(t, nil)

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		s.RunPeriodic(ctx, 0) // interval <= 0 falls back to PollInterval (15m)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic did not return promptly after ctx was canceled")
	}
}
