package musicbrainz

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := NewClient("0.1.0-test", "test@example.com")
	c.baseURL = srv.URL
	c.minInterval = time.Millisecond    // don't slow down tests with the real 1.1s throttle
	c.retryBaseDelay = time.Millisecond // nor the real retry backoff
	return c
}

const sampleRecordingJSON = `{
	"id": "3a714783-ab2f-4db9-81c8-34b623eda833",
	"title": "Alpha and Omega",
	"length": 422533,
	"score": 100,
	"artist-credit": [
		{
			"name": "Boards of Canada",
			"artist": {
				"id": "69158f97-4c07-4c4e-baf8-4e4ab1ed666e",
				"name": "Boards of Canada",
				"sort-name": "Boards of Canada"
			}
		}
	],
	"releases": [
		{
			"id": "1f2614d4-c7a3-4ba6-929a-0e5570c96768",
			"title": "Geogaddi",
			"date": "2002-02-04",
			"release-group": {
				"id": "f2ed9d82-874a-337a-9878-f4ae18102661",
				"title": "Geogaddi",
				"primary-type": "Album"
			}
		}
	]
}`

func TestLookupRecording(t *testing.T) {
	var gotPath, gotUserAgent, gotInc string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUserAgent = r.Header.Get("User-Agent")
		gotInc = r.URL.Query().Get("inc")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleRecordingJSON))
	})

	rec, err := c.LookupRecording(t.Context(), "3a714783-ab2f-4db9-81c8-34b623eda833")
	if err != nil {
		t.Fatalf("LookupRecording: %v", err)
	}

	if gotPath != "/recording/3a714783-ab2f-4db9-81c8-34b623eda833" {
		t.Errorf("request path = %q", gotPath)
	}
	if !strings.Contains(gotUserAgent, "CantiNode/0.1.0-test") || !strings.Contains(gotUserAgent, "test@example.com") {
		t.Errorf("User-Agent = %q, want it to identify CantiNode and the contact email", gotUserAgent)
	}
	if gotInc == "" {
		t.Error("expected an inc= query parameter requesting artist-credits/releases")
	}

	if rec.Title != "Alpha and Omega" {
		t.Errorf("Title = %q", rec.Title)
	}
	if rec.PrimaryArtist().Name != "Boards of Canada" {
		t.Errorf("PrimaryArtist().Name = %q", rec.PrimaryArtist().Name)
	}
	rel := rec.BestRelease("")
	if rel.Title != "Geogaddi" {
		t.Errorf("BestRelease().Title = %q", rel.Title)
	}
	if rel.ReleaseGroup.PrimaryType != "Album" {
		t.Errorf("ReleaseGroup.PrimaryType = %q", rel.ReleaseGroup.PrimaryType)
	}
}

// sampleSeriesJSON is shaped like the real payload verified live against
// MusicBrainz's own API for the "Now That's What I Call Music!" series
// (GET /series/{mbid}?inc=release-group-rels+artist-credits&fmt=json):
// relations out of ordering-key order (MusicBrainz doesn't guarantee any
// particular order), one relation whose target-type isn't a release group
// at all (must be filtered out), and a release group whose primary-type
// comes back JSON null (observed live — must decode cleanly to "").
const sampleSeriesJSON = `{
	"id": "d223e2e2-e90b-4d88-b637-4215b7ebaac2",
	"type": "Release group series",
	"disambiguation": "USA",
	"name": "Now That’s What I Call Music!",
	"relations": [
		{
			"target-type": "release_group",
			"ordering-key": 84,
			"release_group": {
				"id": "c08490f1-fa82-407e-8731-4a2e17840a6a",
				"title": "NOW That's What I Call Music 84",
				"primary-type": null,
				"secondary-types": [],
				"first-release-date": "2022-10-28",
				"artist-credit": [
					{
						"name": "Various Artists",
						"artist": {
							"id": "89ad4ac3-39f7-470e-963a-56509c546377",
							"name": "Various Artists",
							"sort-name": "Various Artists"
						}
					}
				]
			}
		},
		{
			"target-type": "release_group",
			"ordering-key": 1,
			"release_group": {
				"id": "c8f39a5b-e9b0-3b9e-9292-3c4527ecb61f",
				"title": "NOW",
				"primary-type": null,
				"secondary-types": [],
				"first-release-date": "1998-10-27",
				"artist-credit": [
					{
						"name": "Various Artists",
						"artist": {
							"id": "89ad4ac3-39f7-470e-963a-56509c546377",
							"name": "Various Artists",
							"sort-name": "Various Artists"
						}
					}
				]
			}
		},
		{
			"target-type": "artist",
			"ordering-key": 1,
			"artist": {
				"id": "89ad4ac3-39f7-470e-963a-56509c546377",
				"name": "Various Artists"
			}
		}
	]
}`

func TestLookupSeries(t *testing.T) {
	var gotPath, gotInc string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInc = r.URL.Query().Get("inc")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleSeriesJSON))
	})

	s, err := c.LookupSeries(t.Context(), "d223e2e2-e90b-4d88-b637-4215b7ebaac2")
	if err != nil {
		t.Fatalf("LookupSeries: %v", err)
	}
	if gotPath != "/series/d223e2e2-e90b-4d88-b637-4215b7ebaac2" {
		t.Errorf("request path = %q", gotPath)
	}
	if !strings.Contains(gotInc, "release-group-rels") || !strings.Contains(gotInc, "artist-credits") {
		t.Errorf("inc = %q, want release-group-rels and artist-credits", gotInc)
	}

	if s.Name != "Now That’s What I Call Music!" || s.Type != "Release group series" {
		t.Errorf("Name/Type = %q/%q", s.Name, s.Type)
	}
	if len(s.Relations) != 2 {
		t.Fatalf("len(Relations) = %d, want 2 (the artist relation must be filtered out)", len(s.Relations))
	}
	// Sorted by ordering-key ascending, regardless of the response's own order.
	if s.Relations[0].OrderingKey != 1 || s.Relations[0].Title != "NOW" {
		t.Errorf("Relations[0] = %+v, want ordering-key 1 (NOW)", s.Relations[0])
	}
	if s.Relations[1].OrderingKey != 84 || s.Relations[1].Title != "NOW That's What I Call Music 84" {
		t.Errorf("Relations[1] = %+v, want ordering-key 84", s.Relations[1])
	}
	if s.Relations[1].ReleaseGroupMBID != "c08490f1-fa82-407e-8731-4a2e17840a6a" {
		t.Errorf("ReleaseGroupMBID = %q", s.Relations[1].ReleaseGroupMBID)
	}
	if s.Relations[1].PrimaryType != "" {
		t.Errorf("PrimaryType = %q, want empty (JSON null must decode cleanly)", s.Relations[1].PrimaryType)
	}
	if len(s.Relations[1].ArtistCredit) == 0 || s.Relations[1].ArtistCredit[0].Name != "Various Artists" {
		t.Errorf("ArtistCredit = %+v, want Various Artists passed through", s.Relations[1].ArtistCredit)
	}
}

// TestLookupSeriesRejectsSeriesWithNoReleaseGroups covers a real series of
// a kind CantiNode doesn't support (one that links only, say, recordings
// or works) — a 200 response with nothing usable after filtering, which
// must be a clear, distinguishable rejection rather than silently
// returning an empty series.
func TestLookupSeriesRejectsSeriesWithNoReleaseGroups(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "some-work-series-mbid",
			"type": "Work series",
			"name": "Some Work Series",
			"relations": [
				{"target-type": "work", "ordering-key": 1, "work": {"id": "w1"}}
			]
		}`))
	})

	_, err := c.LookupSeries(t.Context(), "some-work-series-mbid")
	if !errors.Is(err, ErrSeriesHasNoReleaseGroups) {
		t.Errorf("err = %v, want ErrSeriesHasNoReleaseGroups", err)
	}
}

func TestSearchRecordings(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"count": 1, "recordings": [` + sampleRecordingJSON + `]}`))
	})

	results, err := c.SearchRecordings(t.Context(), "Boards of Canada", "Geogaddi", "Alpha and Omega")
	if err != nil {
		t.Fatalf("SearchRecordings: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Score != 100 {
		t.Errorf("Score = %d, want 100", results[0].Score)
	}
	for _, want := range []string{`recording:"Alpha and Omega"`, `artist:"Boards of Canada"`, `release:"Geogaddi"`} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q does not contain %q", gotQuery, want)
		}
	}
}

func TestSearchRecordingsRequiresAtLeastOneField(t *testing.T) {
	c := NewClient("0.1.0-test", "")
	if _, err := c.SearchRecordings(t.Context(), "", "", ""); err == nil {
		t.Error("expected an error when artist, release, and title are all empty")
	}
}

func TestBuildRecordingQueryEscapesQuotes(t *testing.T) {
	q := buildRecordingQuery(`Artist "Nickname"`, "", "")
	want := `artist:"Artist \"Nickname\""`
	if q != want {
		t.Errorf("buildRecordingQuery = %q, want %q", q, want)
	}
}

func TestGetNonOKStatusReturnsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("rate limited"))
	})

	_, err := c.LookupRecording(t.Context(), "some-mbid")
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want it to mention status 503", err)
	}
}

func TestGetRetriesTransientStatusThenSucceeds(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"The MusicBrainz web server is currently busy. Please try again later."}`))
			return
		}
		w.Write([]byte(sampleRecordingJSON))
	})

	rec, err := c.LookupRecording(t.Context(), "some-mbid")
	if err != nil {
		t.Fatalf("expected the third attempt to succeed, got: %v", err)
	}
	if rec.Title != "Alpha and Omega" {
		t.Errorf("Title = %q, want the decoded sample response", rec.Title)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want exactly 3 (2 failures + 1 success)", calls)
	}
}

func TestGetGivesUpAfterMaxRetries(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := c.LookupRecording(t.Context(), "some-mbid")
	if err == nil {
		t.Fatal("expected an error once every retry is exhausted")
	}
	if want := maxRetries + 1; calls != want {
		t.Errorf("calls = %d, want %d (the initial attempt plus every retry)", calls, want)
	}
}

func TestGetDoesNotRetryNonTransientStatus(t *testing.T) {
	var calls int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := c.LookupRecording(t.Context(), "some-mbid")
	if err == nil {
		t.Fatal("expected an error on 404")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want exactly 1 — a 404 is never transient, so it must not be retried", calls)
	}
}

func TestThrottleEnforcesMinInterval(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sampleRecordingJSON))
	})
	c.minInterval = 100 * time.Millisecond

	start := time.Now()
	if _, err := c.LookupRecording(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LookupRecording(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < c.minInterval {
		t.Errorf("two requests completed in %v, want at least %v (throttle not enforced)", elapsed, c.minInterval)
	}
}
