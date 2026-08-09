package audiodb

import (
	"net/http"
	"net/http/httptest"
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

const sampleArtistJSON = `{
	"artists": [
		{
			"idArtist": "111239",
			"strArtist": "Coldplay",
			"strBiographyEN": "Coldplay are a British rock band formed in London in 1996.",
			"strArtistThumb": "https://example.com/thumb.jpg",
			"strArtistFanart": "https://example.com/fanart.jpg"
		}
	]
}`

func TestLookupArtistByMBIDReturnsBioAndThumb(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})

	meta, err := c.LookupArtistByMBID(t.Context(), "cc197bad-dc9c-440d-a5b5-d52ba2e14234")
	if err != nil {
		t.Fatalf("LookupArtistByMBID: %v", err)
	}
	if meta == nil {
		t.Fatal("meta = nil, want a result")
	}
	if gotPath != "/test-key/artist-mb.php" {
		t.Errorf("path = %q", gotPath)
	}
	if meta.Bio != "Coldplay are a British rock band formed in London in 1996." {
		t.Errorf("Bio = %q", meta.Bio)
	}
	if meta.ImageURL != "https://example.com/thumb.jpg" {
		t.Errorf("ImageURL = %q, want the thumb (preferred over fanart)", meta.ImageURL)
	}
}

const sampleArtistNoThumbJSON = `{
	"artists": [
		{
			"idArtist": "111239",
			"strArtist": "Coldplay",
			"strBiographyEN": "Bio text.",
			"strArtistThumb": "",
			"strArtistFanart": "https://example.com/fanart.jpg"
		}
	]
}`

func TestLookupArtistByMBIDFallsBackToFanart(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistNoThumbJSON))
	})

	meta, err := c.LookupArtistByMBID(t.Context(), "mbid")
	if err != nil {
		t.Fatalf("LookupArtistByMBID: %v", err)
	}
	if meta.ImageURL != "https://example.com/fanart.jpg" {
		t.Errorf("ImageURL = %q, want fanart fallback", meta.ImageURL)
	}
}

func TestLookupArtistByMBIDNotFoundReturnsNilNotError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"artists": null}`))
	})

	meta, err := c.LookupArtistByMBID(t.Context(), "unknown-mbid")
	if err != nil {
		t.Fatalf("LookupArtistByMBID: %v", err)
	}
	if meta != nil {
		t.Errorf("meta = %+v, want nil for an artist TheAudioDB doesn't have", meta)
	}
}

func TestNewClientFallsBackToPublicTestKey(t *testing.T) {
	c := NewClient("")
	if c.apiKey != publicTestKey {
		t.Errorf("apiKey = %q, want the public test key %q", c.apiKey, publicTestKey)
	}
}

func TestNewClientUsesProvidedKey(t *testing.T) {
	c := NewClient("my-real-key")
	if c.apiKey != "my-real-key" {
		t.Errorf("apiKey = %q, want my-real-key", c.apiKey)
	}
}
