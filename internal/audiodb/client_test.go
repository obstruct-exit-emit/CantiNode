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
			"strBiography": "Coldplay are a British rock band formed in London in 1996.",
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
			"strBiography": "Bio text.",
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

const sampleAlbumJSON = `{
	"album": [
		{
			"idAlbum": "2314525",
			"strAlbum": "Moonglow",
			"strMusicBrainzID": "a9dced89-49cb-4430-ac83-f4973cc71695",
			"strAlbumThumb": "https://r2.theaudiodb.com/images/media/album/thumb/wuysyy1550959319.jpg",
			"strDescription": "Moonglow is the eighth full-length album by Tobias Sammet's metal opera project Avantasia."
		}
	]
}`

func TestLookupAlbumByReleaseGroupMBIDReturnsThumb(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleAlbumJSON))
	})

	meta, err := c.LookupAlbumByReleaseGroupMBID(t.Context(), "a9dced89-49cb-4430-ac83-f4973cc71695")
	if err != nil {
		t.Fatalf("LookupAlbumByReleaseGroupMBID: %v", err)
	}
	if meta == nil {
		t.Fatal("meta = nil, want a result")
	}
	if gotPath != "/test-key/album-mb.php" {
		t.Errorf("path = %q", gotPath)
	}
	if meta.ThumbURL != "https://r2.theaudiodb.com/images/media/album/thumb/wuysyy1550959319.jpg" {
		t.Errorf("ThumbURL = %q", meta.ThumbURL)
	}
	if meta.IDAlbum != "2314525" {
		t.Errorf("IDAlbum = %q, want 2314525", meta.IDAlbum)
	}
	if meta.Description != "Moonglow is the eighth full-length album by Tobias Sammet's metal opera project Avantasia." {
		t.Errorf("Description = %q", meta.Description)
	}
}

func TestLookupAlbumByReleaseGroupMBIDNotFoundReturnsNilNotError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"album": null}`))
	})

	meta, err := c.LookupAlbumByReleaseGroupMBID(t.Context(), "unknown-mbid")
	if err != nil {
		t.Fatalf("LookupAlbumByReleaseGroupMBID: %v", err)
	}
	if meta != nil {
		t.Errorf("meta = %+v, want nil for an album TheAudioDB doesn't have", meta)
	}
}

// TestLookupAlbumByReleaseGroupMBIDReturnsEntryEvenWithoutThumb proves an
// entry with idAlbum but no cover art still comes back as a real result
// (not treated as "not found") — internal/coverart only ever needs
// ThumbURL and already checks it's non-empty before using it, but a
// caller that only wants IDAlbum (linking out to the album's own
// theaudiodb.com page) shouldn't lose that just because this particular
// entry has no thumb.
func TestLookupAlbumByReleaseGroupMBIDReturnsEntryEvenWithoutThumb(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"album": [{"idAlbum": "1", "strAlbum": "X", "strAlbumThumb": ""}]}`))
	})

	meta, err := c.LookupAlbumByReleaseGroupMBID(t.Context(), "mbid")
	if err != nil {
		t.Fatalf("LookupAlbumByReleaseGroupMBID: %v", err)
	}
	if meta == nil {
		t.Fatal("meta = nil, want a result — TheAudioDB does have this album, just no thumb")
	}
	if meta.ThumbURL != "" {
		t.Errorf("ThumbURL = %q, want empty", meta.ThumbURL)
	}
	if meta.IDAlbum != "1" {
		t.Errorf("IDAlbum = %q, want 1", meta.IDAlbum)
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
