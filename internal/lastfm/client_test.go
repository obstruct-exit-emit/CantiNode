package lastfm

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

	c := NewClientWithBaseURL("test-key", srv.URL)
	c.minInterval = time.Millisecond // don't slow down tests with the real throttle
	return c
}

func TestTopArtistsForUser(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"topartists":{"artist":[
			{"name":"Boards of Canada","mbid":"a5f4b3a4-3a5c-4a1a-8f8a-1e1e1e1e1e1e"},
			{"name":"Some Local Band","mbid":""}
		]}}`))
	})

	got, err := c.TopArtistsForUser(t.Context(), "danpa", 10)
	if err != nil {
		t.Fatalf("TopArtistsForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d artists, want 2", len(got))
	}
	if got[0].Name != "Boards of Canada" || got[0].MBID != "a5f4b3a4-3a5c-4a1a-8f8a-1e1e1e1e1e1e" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Name != "Some Local Band" || got[1].MBID != "" {
		t.Errorf("got[1] = %+v, want an empty MBID preserved (not every Last.fm artist links to one)", got[1])
	}
	if !strings.Contains(gotQuery, "method=user.gettopartists") || !strings.Contains(gotQuery, "user=danpa") {
		t.Errorf("query = %q, missing expected params", gotQuery)
	}
}

func TestTopArtistsForTag(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"topartists":{"artist":[{"name":"Slowdive","mbid":"b1f4b3a4-3a5c-4a1a-8f8a-1e1e1e1e1e1e"}]}}`))
	})

	got, err := c.TopArtistsForTag(t.Context(), "shoegaze", 10)
	if err != nil {
		t.Fatalf("TopArtistsForTag: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Slowdive" {
		t.Errorf("got = %+v", got)
	}
	if !strings.Contains(gotQuery, "method=tag.gettopartists") || !strings.Contains(gotQuery, "tag=shoegaze") {
		t.Errorf("query = %q, missing expected params", gotQuery)
	}
}

// TestTopArtistsSurfacesLastFMError is the regression test for a real
// Last.fm API quirk: an API-level failure (bad key, unknown user/tag)
// comes back as an ordinary JSON body with an "error"/"message" pair, not
// a non-2xx HTTP status alone — a caller checking only the status code
// would silently decode a zero-artist result instead of surfacing the
// real reason.
func TestTopArtistsSurfacesLastFMError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":10,"message":"Invalid API key"}`))
	})

	_, err := c.TopArtistsForUser(t.Context(), "danpa", 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error = %v, want it to surface Last.fm's own message", err)
	}
}

func TestTopArtistsRequiresAPIKey(t *testing.T) {
	c := NewClient("")
	if _, err := c.TopArtistsForUser(t.Context(), "danpa", 10); err == nil {
		t.Fatal("expected an error for a missing API key")
	}
}
