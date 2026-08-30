package importlist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/discography"
	"github.com/cantinode/cantinode/internal/lastfm"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// newTestService wires a Service against a real (temp-file) music store and
// a mocked MusicBrainz server; mbHandler is nil-safe (a nil handler means
// "this test never expects a MusicBrainz request"). Also returns the
// backing *sql.DB and the import-list Store, so a test can seed rows
// through the same Store the Service itself uses.
func newTestService(t *testing.T, mbHandler http.HandlerFunc, lastfmHandler http.HandlerFunc) (*Service, *musiclibrary.Store, *Store) {
	t.Helper()

	if mbHandler == nil {
		mbHandler = func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected MusicBrainz request: %s", r.URL.String())
		}
	}
	mbSrv := httptest.NewServer(mbHandler)
	t.Cleanup(mbSrv.Close)
	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", mbSrv.URL)

	var lf *lastfm.Client
	if lastfmHandler != nil {
		lfSrv := httptest.NewServer(lastfmHandler)
		t.Cleanup(lfSrv.Close)
		lf = lastfm.NewClientWithBaseURL("test-key", lfSrv.URL)
	} else {
		lf = lastfm.NewClient("")
	}

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	music := musiclibrary.NewStore(db)
	disc := discography.New(mb, music)
	store := NewStore(db)

	return New(store, mb, music, disc, lf), music, store
}

func TestResolveMusicBrainzSeriesDedupesAndSkipsVariousArtists(t *testing.T) {
	s, _, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/series/series-mbid" {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "series-mbid", "name": "NOW",
			"relations": []map[string]any{
				{
					"target-type": "release_group", "ordering-key": 1,
					"release_group": map[string]any{
						"id": "rg-1", "title": "NOW 1", "primary-type": "Album",
						"artist-credit": []map[string]any{{"name": "Phil Collins", "artist": map[string]any{"id": "artist-1", "name": "Phil Collins"}}},
					},
				},
				{
					"target-type": "release_group", "ordering-key": 2,
					"release_group": map[string]any{
						"id": "rg-2", "title": "NOW 2", "primary-type": "Album",
						"artist-credit": []map[string]any{{"name": "Phil Collins", "artist": map[string]any{"id": "artist-1", "name": "Phil Collins"}}},
					},
				},
				{
					"target-type": "release_group", "ordering-key": 3,
					"release_group": map[string]any{
						"id": "rg-3", "title": "NOW 3", "primary-type": "Album",
						"artist-credit": []map[string]any{{"name": "Various Artists", "artist": map[string]any{"id": musicbrainz.VariousArtistsMBID, "name": "Various Artists"}}},
					},
				},
			},
		})
	}, nil)

	mbids, err := s.Resolve(context.Background(), ImportList{Type: TypeMusicBrainzSeries, SeriesMBID: "series-mbid"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(mbids) != 1 || mbids[0] != "artist-1" {
		t.Errorf("mbids = %v, want [artist-1] (deduped, Various Artists excluded)", mbids)
	}
}

func TestResolvePlainListSearchesEachName(t *testing.T) {
	var gotQueries []string
	s, _, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		gotQueries = append(gotQueries, r.URL.Query().Get("query"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artists": []map[string]any{{"id": "resolved-" + r.URL.Query().Get("query"), "name": "x"}},
		})
	}, nil)

	il := ImportList{Type: TypeList, ListText: "Boards of Canada\n\n# a comment\nAphex Twin"}
	mbids, err := s.Resolve(context.Background(), il)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(mbids) != 2 {
		t.Fatalf("mbids = %v, want 2 (comment/blank line skipped)", mbids)
	}
	if len(gotQueries) != 2 {
		t.Errorf("queries = %v, want exactly 2 searches", gotQueries)
	}
}

func TestResolvePlainListFetchesSourceURL(t *testing.T) {
	listSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Boards of Canada\nAphex Twin\n"))
	}))
	defer listSrv.Close()

	s, _, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "resolved-" + r.URL.Query().Get("query"), "name": "x"}}})
	}, nil)

	il := ImportList{Type: TypeList, SourceURL: listSrv.URL, ListText: "this should be ignored"}
	mbids, err := s.Resolve(context.Background(), il)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(mbids) != 2 {
		t.Errorf("mbids = %v, want 2 (fetched from SourceURL, not ListText)", mbids)
	}
}

func TestResolveLastFMUsesMBIDWhenPresentAndSearchesWhenAbsent(t *testing.T) {
	var searchedNames []string
	s, _, _ := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		searchedNames = append(searchedNames, r.URL.Query().Get("query"))
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "searched-mbid", "name": "x"}}})
	}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"topartists":{"artist":[
			{"name":"Boards of Canada","mbid":"direct-mbid"},
			{"name":"No MBID Band","mbid":""}
		]}}`))
	})

	il := ImportList{Type: TypeLastFM, LastfmKind: LastfmKindUser, LastfmTarget: "danpa"}
	mbids, err := s.Resolve(context.Background(), il)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(mbids) != 2 || mbids[0] != "direct-mbid" || mbids[1] != "searched-mbid" {
		t.Errorf("mbids = %v, want [direct-mbid searched-mbid]", mbids)
	}
	if len(searchedNames) != 1 || !strings.Contains(searchedNames[0], "No MBID Band") {
		t.Errorf("searchedNames = %v, want a MusicBrainz search only for the artist with no Last.fm mbid", searchedNames)
	}
}

// TestPollOnceAddsAndMonitorsResolvedArtists is the end-to-end regression
// test for the feature's whole point: a resolved MBID not already
// monitored gets added, monitored, and has its discography cached — the
// same outcome a manual "+Add artist" click produces.
func TestPollOnceAddsAndMonitorsResolvedArtists(t *testing.T) {
	// Exercised via a musicbrainz_series list so only LookupSeries +
	// LookupArtist + BrowseArtistReleaseGroups need mocking.
	s, music, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/series/series-mbid":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "series-mbid", "name": "NOW",
				"relations": []map[string]any{
					{
						"target-type": "release_group", "ordering-key": 1,
						"release_group": map[string]any{
							"id": "rg-1", "title": "NOW 1", "primary-type": "Album",
							"artist-credit": []map[string]any{{"name": "Boards of Canada", "artist": map[string]any{"id": "artist-1", "name": "Boards of Canada"}}},
						},
					},
				},
			})
		case "/artist/artist-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-1", "name": "Boards of Canada", "sort-name": "Boards of Canada"})
		case "/release-group/":
			_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}, nil)

	il := &ImportList{Name: "My Series", Type: TypeMusicBrainzSeries, SeriesMBID: "series-mbid", Enabled: true}
	if err := store.Add(il); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 1 || result.Added != 1 {
		t.Fatalf("result = %+v, want Checked=1 Added=1", result)
	}

	artists, err := music.ListArtists()
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 1 || artists[0].MBID != "artist-1" || !artists[0].IsMonitored {
		t.Fatalf("artists = %+v, want one monitored artist-1", artists)
	}

	synced, err := store.Get(il.ID)
	if err != nil {
		t.Fatal(err)
	}
	if synced.LastSyncedAt == "" || synced.LastSyncError != "" {
		t.Errorf("synced = %+v, want LastSyncedAt set and no error", synced)
	}
}

// TestPollOnceSkipsAlreadyMonitoredArtist proves the optimization in
// PollOnce's own doc comment: an MBID already monitored never triggers a
// second LookupArtist round trip.
func TestPollOnceSkipsAlreadyMonitoredArtist(t *testing.T) {
	var lookupArtistCalls int
	s, music, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/series/series-mbid":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "series-mbid", "name": "NOW",
				"relations": []map[string]any{
					{
						"target-type": "release_group", "ordering-key": 1,
						"release_group": map[string]any{
							"id": "rg-1", "title": "NOW 1", "primary-type": "Album",
							"artist-credit": []map[string]any{{"name": "Boards of Canada", "artist": map[string]any{"id": "artist-1", "name": "Boards of Canada"}}},
						},
					},
				},
			})
		case "/artist/artist-1":
			lookupArtistCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-1", "name": "Boards of Canada"})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}, nil)

	if _, err := music.GetOrCreateArtist("artist-1", "Boards of Canada", "Boards of Canada"); err != nil {
		t.Fatal(err)
	}
	a, err := music.GetArtist(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := music.SetArtistMonitored(a.ID, true); err != nil {
		t.Fatal(err)
	}

	il := &ImportList{Name: "My Series", Type: TypeMusicBrainzSeries, SeriesMBID: "series-mbid", Enabled: true}
	if err := store.Add(il); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Added != 0 {
		t.Errorf("Added = %d, want 0 (already monitored)", result.Added)
	}
	if lookupArtistCalls != 0 {
		t.Errorf("LookupArtist called %d times, want 0 for an already-monitored artist", lookupArtistCalls)
	}
}

// TestPollOneListFailureDoesNotStopOthers mirrors
// internal/discoveryrefresh's own non-aborting sweep pattern: one list's
// resolve failure must not prevent another list's artists from being
// added.
func TestPollOneListFailureDoesNotStopOthers(t *testing.T) {
	s, _, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/series/bad-series":
			w.WriteHeader(http.StatusNotFound)
		case "/series/good-series":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "good-series", "name": "Good",
				"relations": []map[string]any{
					{
						"target-type": "release_group", "ordering-key": 1,
						"release_group": map[string]any{
							"id": "rg-1", "title": "Album", "primary-type": "Album",
							"artist-credit": []map[string]any{{"name": "Boards of Canada", "artist": map[string]any{"id": "artist-1", "name": "Boards of Canada"}}},
						},
					},
				},
			})
		case "/artist/artist-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-1", "name": "Boards of Canada"})
		case "/release-group/":
			_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}, nil)

	bad := &ImportList{Name: "Bad", Type: TypeMusicBrainzSeries, SeriesMBID: "bad-series", Enabled: true}
	good := &ImportList{Name: "Good", Type: TypeMusicBrainzSeries, SeriesMBID: "good-series", Enabled: true}
	if err := store.Add(bad); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(good); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Checked != 2 || result.Added != 1 {
		t.Fatalf("result = %+v, want Checked=2 Added=1", result)
	}

	badRow, err := store.Get(bad.ID)
	if err != nil {
		t.Fatal(err)
	}
	if badRow.LastSyncError == "" {
		t.Error("bad list should have a recorded sync error")
	}
	goodRow, err := store.Get(good.ID)
	if err != nil {
		t.Fatal(err)
	}
	if goodRow.LastSyncError != "" {
		t.Errorf("good list's sync error = %q, want empty", goodRow.LastSyncError)
	}
}

// TestRunPeriodicSweepsImmediatelyThenOnSchedule mirrors
// internal/discoveryrefresh's own RunPeriodic test shape.
func TestRunPeriodicSweepsImmediatelyThenOnSchedule(t *testing.T) {
	s, _, _ := newTestService(t, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	sweeps := make(chan struct{}, 4)
	next := func(now time.Time) time.Time {
		select {
		case sweeps <- struct{}{}:
		default:
		}
		return now.Add(10 * time.Millisecond)
	}

	go s.RunPeriodic(ctx, next)

	select {
	case <-sweeps:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic did not sweep immediately on start")
	}
	select {
	case <-sweeps:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic did not sweep again on schedule")
	}
	cancel()
}
