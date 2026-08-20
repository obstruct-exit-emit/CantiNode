package coverart

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/audiodb"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := NewClient(t.TempDir(), "cantinode-test/0.1", nil)
	c.baseURL = srv.URL
	return c
}

func TestGetFrontCoverFetchesAndCaches(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/release/release-mbid/front-250" {
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("fake jpeg bytes"))
	})

	path, ct, err := c.GetFrontCover(t.Context(), "", "release-mbid")
	if err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	if ct != "image/jpeg" {
		t.Errorf("contentType = %q, want image/jpeg", ct)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake jpeg bytes" {
		t.Errorf("cached file content = %q", data)
	}

	// Second call must be served from cache, not hit the server again.
	path2, ct2, err := c.GetFrontCover(t.Context(), "", "release-mbid")
	if err != nil {
		t.Fatalf("GetFrontCover (cached): %v", err)
	}
	if path2 != path || ct2 != ct {
		t.Errorf("cached call returned (%q,%q), want (%q,%q)", path2, ct2, path, ct)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (second call should hit the cache)", requests)
	}
}

func TestGetFrontCoverCaches404AsNoCoverArt(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	})

	_, _, err := c.GetFrontCover(t.Context(), "", "release-mbid")
	if !errors.Is(err, ErrNoCoverArt) {
		t.Fatalf("err = %v, want ErrNoCoverArt", err)
	}

	// Second call must be served from the cached sentinel, not hit the
	// server again.
	_, _, err = c.GetFrontCover(t.Context(), "", "release-mbid")
	if !errors.Is(err, ErrNoCoverArt) {
		t.Fatalf("second call err = %v, want ErrNoCoverArt", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (second call should hit the sentinel cache)", requests)
	}
}

// TestGetFrontCoverRechecksStaleNoCoverSentinel is the regression test for
// a real bug found live: a "no cover art" sentinel was cached permanently,
// but Cover Art Archive's own catalog isn't static — a real release
// (found live: a "Cities 97 Sampler" volume) had no cover art when first
// checked and had one added later, yet CantiNode kept serving the stale
// 404 forever with no way to notice. A sentinel older than
// noCoverRecheckAfter must be treated as stale and rechecked live.
func TestGetFrontCoverRechecksStaleNoCoverSentinel(t *testing.T) {
	var requests int
	var has404 = true
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if has404 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("fake jpeg bytes"))
	})

	if _, _, err := c.GetFrontCover(t.Context(), "", "release-mbid"); !errors.Is(err, ErrNoCoverArt) {
		t.Fatalf("first call err = %v, want ErrNoCoverArt", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}

	// Backdate the sentinel past noCoverRecheckAfter — standing in for
	// "30 days have passed" without an actual 30-day-long test.
	sentinel := filepath.Join(c.cacheDir, "release-mbid"+noCoverSentinelExt)
	stale := time.Now().Add(-noCoverRecheckAfter - time.Hour)
	if err := os.Chtimes(sentinel, stale, stale); err != nil {
		t.Fatal(err)
	}

	// Cover Art Archive now genuinely has the art — the real-world case
	// this fix exists for.
	has404 = false
	path, _, err := c.GetFrontCover(t.Context(), "", "release-mbid")
	if err != nil {
		t.Fatalf("GetFrontCover after the sentinel went stale: %v", err)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 — a stale sentinel must trigger a real recheck, not keep serving the old miss", requests)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("cached cover file missing: %v", err)
	}
}

func TestGetFrontCoverServerErrorIsNotCached(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, _, err := c.GetFrontCover(t.Context(), "", "release-mbid"); err == nil {
		t.Fatal("expected an error on a 500 response")
	}
	if _, _, err := c.GetFrontCover(t.Context(), "", "release-mbid"); err == nil {
		t.Fatal("expected an error on the second attempt too")
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (a transient error must not be cached)", requests)
	}
}

func TestGetFrontCoverRequiresReleaseMBID(t *testing.T) {
	c := NewClient(t.TempDir(), "cantinode-test/0.1", nil)
	if _, _, err := c.GetFrontCover(t.Context(), "", ""); err == nil {
		t.Error("expected an error for an empty releaseMBID")
	}
}

func TestGetFrontCoverCacheDirIsCreatedOnDemand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "covers")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("data"))
	})
	c.cacheDir = dir

	if _, _, err := c.GetFrontCover(t.Context(), "", "release-mbid"); err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("cache dir was not created: %v", err)
	}
}

// audioDBAlbumJSON builds a TheAudioDB /album-mb.php response whose thumb
// URL points back at thumbURL (typically a path on the same test server).
func audioDBAlbumJSON(thumbURL string) string {
	if thumbURL == "" {
		return `{"album": null}`
	}
	return `{"album": [{"idAlbum": "1", "strAlbum": "X", "strAlbumThumb": "` + thumbURL + `"}]}`
}

func TestGetFrontCoverPrefersAudioDBOverCoverArtArchive(t *testing.T) {
	caaRequests := 0
	caa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caaRequests++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(caa.Close)

	var audiodbSrv *httptest.Server
	audiodbSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/thumb.jpg" {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write([]byte("audiodb thumb bytes"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(audioDBAlbumJSON(audiodbSrv.URL + "/thumb.jpg")))
	}))
	t.Cleanup(audiodbSrv.Close)

	adb := audiodb.NewClientWithBaseURL("test-key", audiodbSrv.URL)
	c := NewClient(t.TempDir(), "cantinode-test/0.1", adb)
	c.baseURL = caa.URL

	path, ct, err := c.GetFrontCover(t.Context(), "release-group-mbid", "release-mbid")
	if err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	if ct != "image/jpeg" {
		t.Errorf("contentType = %q", ct)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "audiodb thumb bytes" {
		t.Errorf("cached content = %q, want TheAudioDB's own thumb", data)
	}
	if caaRequests != 0 {
		t.Errorf("Cover Art Archive was queried %d times, want 0 — TheAudioDB already had the art", caaRequests)
	}
}

func TestGetFrontCoverFallsBackToCoverArtArchiveWhenAudioDBHasNothing(t *testing.T) {
	caa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("caa cover bytes"))
	}))
	t.Cleanup(caa.Close)

	audiodbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(audioDBAlbumJSON(""))) // TheAudioDB has nothing for this release group
	}))
	t.Cleanup(audiodbSrv.Close)

	adb := audiodb.NewClientWithBaseURL("test-key", audiodbSrv.URL)
	c := NewClient(t.TempDir(), "cantinode-test/0.1", adb)
	c.baseURL = caa.URL

	path, _, err := c.GetFrontCover(t.Context(), "release-group-mbid", "release-mbid")
	if err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "caa cover bytes" {
		t.Errorf("cached content = %q, want the Cover Art Archive fallback", data)
	}
}

// TestGetFrontCoverSkipsAudioDBWithoutReleaseGroupMBID covers a caller
// that only has the release MBID in hand (releaseGroupMBID == "") — must
// go straight to Cover Art Archive without ever querying TheAudioDB.
func TestGetFrontCoverSkipsAudioDBWithoutReleaseGroupMBID(t *testing.T) {
	caa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("caa cover bytes"))
	}))
	t.Cleanup(caa.Close)

	audiodbRequests := 0
	audiodbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		audiodbRequests++
		w.Write([]byte(`{"album": null}`))
	}))
	t.Cleanup(audiodbSrv.Close)

	adb := audiodb.NewClientWithBaseURL("test-key", audiodbSrv.URL)
	c := NewClient(t.TempDir(), "cantinode-test/0.1", adb)
	c.baseURL = caa.URL

	if _, _, err := c.GetFrontCover(t.Context(), "", "release-mbid"); err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	if audiodbRequests != 0 {
		t.Errorf("TheAudioDB was queried %d times, want 0 without a release group mbid", audiodbRequests)
	}
}
