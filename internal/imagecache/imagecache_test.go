package imagecache

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pngBytes is a valid PNG signature (enough for http.DetectContentType) plus
// a tag so different fixtures compare unequal.
func pngBytes(tag string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(tag)...)
}

func TestFetchDownloadsCachesAndSurvivesOrigin(t *testing.T) {
	img := pngBytes("cover")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(img)
	}))
	url := srv.URL + "/cover.png"
	c := New(t.TempDir())

	// First fetch downloads and stores.
	data, ct, err := c.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(data, img) || ct != "image/png" {
		t.Fatalf("fetch = %q (%s), want the png", data, ct)
	}
	if hits != 1 {
		t.Fatalf("origin hits = %d, want 1", hits)
	}

	// Second fetch is served from cache — the origin isn't hit again.
	if _, _, err := c.Fetch(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("origin hits after cached fetch = %d, want 1", hits)
	}

	// With the origin gone, the cache still serves it (URL-rot protection).
	srv.Close()
	data, _, ok := c.Read(url)
	if !ok || !bytes.Equal(data, img) {
		t.Fatalf("cached read after origin close: ok=%v data=%q", ok, data)
	}
}

func TestFetchRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer srv.Close()
	c := New(t.TempDir())
	if _, _, err := c.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected an error for a non-image response")
	}
	if _, _, ok := c.Read(srv.URL); ok {
		t.Fatal("a non-image response must not be cached")
	}
}

func TestFetchRejectsBadScheme(t *testing.T) {
	c := New(t.TempDir())
	if _, _, err := c.Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("expected an error for a non-http scheme")
	}
}

func TestClear(t *testing.T) {
	img := pngBytes("art")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(img)
	}))
	defer srv.Close()
	c := New(t.TempDir())

	// Empty cache clears cleanly.
	if n, _, err := c.Clear(); err != nil || n != 0 {
		t.Fatalf("Clear on empty = %d, %v", n, err)
	}

	// Cache two images, then clear them.
	for _, p := range []string{"/a.png", "/b.png"} {
		if _, _, err := c.Fetch(context.Background(), srv.URL+p); err != nil {
			t.Fatal(err)
		}
	}
	removed, freed, err := c.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 || freed != int64(2*len(img)) {
		t.Fatalf("Clear = %d files, %d bytes; want 2, %d", removed, freed, 2*len(img))
	}
	if _, _, ok := c.Read(srv.URL + "/a.png"); ok {
		t.Fatal("image still cached after Clear")
	}
}
