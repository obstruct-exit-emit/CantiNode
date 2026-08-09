package coverart

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := NewClient(t.TempDir(), "cantinode-test/0.1")
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

	path, ct, err := c.GetFrontCover(t.Context(), "release-mbid")
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
	path2, ct2, err := c.GetFrontCover(t.Context(), "release-mbid")
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

	_, _, err := c.GetFrontCover(t.Context(), "release-mbid")
	if !errors.Is(err, ErrNoCoverArt) {
		t.Fatalf("err = %v, want ErrNoCoverArt", err)
	}

	// Second call must be served from the cached sentinel, not hit the
	// server again.
	_, _, err = c.GetFrontCover(t.Context(), "release-mbid")
	if !errors.Is(err, ErrNoCoverArt) {
		t.Fatalf("second call err = %v, want ErrNoCoverArt", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (second call should hit the sentinel cache)", requests)
	}
}

func TestGetFrontCoverServerErrorIsNotCached(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, _, err := c.GetFrontCover(t.Context(), "release-mbid"); err == nil {
		t.Fatal("expected an error on a 500 response")
	}
	if _, _, err := c.GetFrontCover(t.Context(), "release-mbid"); err == nil {
		t.Fatal("expected an error on the second attempt too")
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (a transient error must not be cached)", requests)
	}
}

func TestGetFrontCoverRequiresReleaseMBID(t *testing.T) {
	c := NewClient(t.TempDir(), "cantinode-test/0.1")
	if _, _, err := c.GetFrontCover(t.Context(), ""); err == nil {
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

	if _, _, err := c.GetFrontCover(t.Context(), "release-mbid"); err != nil {
		t.Fatalf("GetFrontCover: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("cache dir was not created: %v", err)
	}
}
