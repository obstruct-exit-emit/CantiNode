package musicbrainz

import (
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
