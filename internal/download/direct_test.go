package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// waitDone polls the direct client until the item leaves the active states.
func waitDone(t *testing.T, d *direct, id string) Item {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items, err := d.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.ID == id && it.Status != "queued" && it.Status != "downloading" {
				return it
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("download never finished")
	return Item{}
}

func newTestDirect(t *testing.T) *direct {
	t.Helper()
	return newDirect(&ClientConfig{ID: 1, Name: "Fetcher", Type: TypeDirect, Host: t.TempDir()})
}

// TestDirectDownloadsFile: a plain URL streams to the folder, completes with
// the file's path, and Remove(deleteData) deletes it.
func TestDirectDownloadsFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/epub+zip")
		_, _ = w.Write([]byte("epub-bytes"))
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/book", "A Book Title")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" {
		t.Fatalf("status = %q, want completed", it.Status)
	}
	if !strings.HasSuffix(it.Path, "A Book Title.epub") {
		t.Errorf("path = %q, want .epub named after the title", it.Path)
	}
	body, err := os.ReadFile(it.Path)
	if err != nil || string(body) != "epub-bytes" {
		t.Fatalf("file contents = %q, %v", body, err)
	}

	if err := d.Remove(context.Background(), id, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(it.Path); !os.IsNotExist(err) {
		t.Error("Remove(deleteData) should have deleted the file")
	}
}

// TestDirectMirrorFailover: the first mirror 503s; the second delivers.
func TestDirectMirrorFailover(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pdf-bytes"))
	}))
	defer good.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), dead.URL+"/a.pdf|"+good.URL+"/b.pdf", "Mirrored Book")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" || !strings.HasSuffix(it.Path, ".pdf") {
		t.Fatalf("item = %+v, want completed .pdf via mirror", it)
	}
}

// TestDirectFollowsJSONDownloadURL: a membership-API answer (JSON naming the
// real file) is followed one hop.
func TestDirectFollowsJSONDownloadURL(t *testing.T) {
	var fileURL string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/file") {
			_, _ = w.Write([]byte("the-actual-book"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"download_url": "` + fileURL + `"}`))
	}))
	defer api.Close()
	fileURL = api.URL + "/file/book.epub"

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), api.URL+"/dyn/api/fast_download.json?md5=x&key=secret", "Keyed Book")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" || !strings.HasSuffix(it.Path, ".epub") {
		t.Fatalf("item = %+v, want completed .epub via JSON hop", it)
	}
	body, _ := os.ReadFile(it.Path)
	if string(body) != "the-actual-book" {
		t.Errorf("file = %q, want the real file, not the JSON envelope", body)
	}
}

// TestDirectJSONErrorAndSecretRedaction: a JSON error answer fails the
// download, and the membership key never appears in the failure message.
func TestDirectJSONErrorAndSecretRedaction(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error": "Invalid secret key"}`))
	}))
	defer api.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), api.URL+"/fast_download.json?md5=x&key=super-secret-key", "No Luck")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "failed" {
		t.Fatalf("status = %q, want failed", it.Status)
	}
	d.mu.Lock()
	msg := d.items[id].err
	d.mu.Unlock()
	if strings.Contains(msg, "super-secret-key") {
		t.Errorf("failure message leaks the key: %q", msg)
	}
}

// TestDirectFollowsHTMLLandingPage: an open-mirror landing page (a "GET"
// anchor pointing at the real file, library.lol style) is followed to the
// file; a second mirror shape (get.php href) works too.
func TestDirectFollowsHTMLLandingPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main/abc123":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><h1>Title</h1><a href="/file/book%20title.epub"><h2>GET</h2></a><a href="/ipfs/xyz">IPFS</a></html>`))
		case "/ads.php":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><td><a href="get.php?md5=abc&key=x">GET</a></td></html>`))
		case "/get.php":
			_, _ = w.Write([]byte("mirror-two-bytes"))
		case "/file/book title.epub", "/file/book%20title.epub":
			w.Header().Set("Content-Type", "application/epub+zip")
			_, _ = w.Write([]byte("landing-page-book"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/main/abc123", "Landing Book")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" || !strings.HasSuffix(it.Path, ".epub") {
		t.Fatalf("item = %+v, want completed .epub via the GET link", it)
	}
	body, _ := os.ReadFile(it.Path)
	if string(body) != "landing-page-book" {
		t.Errorf("file = %q, want the real file, not the landing page", body)
	}

	// The ads.php/get.php shape resolves too.
	id2, err := d.Add(context.Background(), srv.URL+"/ads.php?md5=abc", "Mirror Two")
	if err != nil {
		t.Fatalf("Add(ads.php): %v", err)
	}
	it2 := waitDone(t, d, id2)
	if it2.Status != "completed" {
		t.Fatalf("ads.php item = %+v", it2)
	}
	body2, _ := os.ReadFile(it2.Path)
	if string(body2) != "mirror-two-bytes" {
		t.Errorf("mirror-two file = %q", body2)
	}
}

// TestDirectHTMLWithoutFileLinkFails: a page with no recognizable download
// link fails that mirror (and the whole download when it's the only one).
func TestDirectHTMLWithoutFileLinkFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><a href="/about">About</a><a href="/search">Search</a></html>`))
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/page", "No Link Here")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if it := waitDone(t, d, id); it.Status != "failed" {
		t.Fatalf("status = %q, want failed", it.Status)
	}
}

// TestDirectNamesFileByContentNotPhpURL reproduces the libgen get.php chain: the
// file is served from a ".../get.php?..." URL as application/octet-stream, but
// its bytes are a real epub. The saved file must be named ".epub" from the
// content sniff — never ".php" from the URL path, the bug that made a perfectly
// good ebook unimportable.
func TestDirectNamesFileByContentNotPhpURL(t *testing.T) {
	epub := append([]byte("PK\x03\x04\x14\x00\x00\x00\x00\x00"),
		[]byte("________________mimetypeapplication/epub+zip then the book bytes")...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(epub)
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/get.php?md5=abc&key=xyz", "Php Named Book")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" {
		t.Fatalf("status = %q, want completed", it.Status)
	}
	if strings.HasSuffix(it.Path, ".php") {
		t.Fatalf("saved as .php — the URL-path extension bug is back: %q", it.Path)
	}
	if !strings.HasSuffix(it.Path, ".epub") {
		t.Errorf("path = %q, want .epub from the content sniff", it.Path)
	}
}

// TestDirectDispositionBeatsPhpURL: when the content isn't sniffable, the
// Content-Disposition filename names the file — the get.php URL's ".php" is
// rejected by the media-extension allowlist rather than becoming the extension.
func TestDirectDispositionBeatsPhpURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="Some Book (2016) - libgen.li.epub"`)
		_, _ = w.Write([]byte("just some bytes with no recognizable magic header"))
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/get.php?md5=abc", "Disp Book")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "completed" || !strings.HasSuffix(it.Path, ".epub") {
		t.Fatalf("item = %+v, want completed .epub from Content-Disposition", it)
	}
	if strings.HasSuffix(it.Path, ".php") {
		t.Errorf("saved as .php: %q", it.Path)
	}
}

// TestDirectRejectsWebPageServedAsFile: a mirror that answers an error/landing
// page but labels it application/octet-stream (slipping past the Content-Type
// guard) must be rejected — not saved as a bogus "book" the importer can't use.
func TestDirectRejectsWebPageServedAsFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Rate limited, try later</body></html>"))
	}))
	defer srv.Close()

	d := newTestDirect(t)
	id, err := d.Add(context.Background(), srv.URL+"/get.php?md5=abc", "Sneaky Page")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	it := waitDone(t, d, id)
	if it.Status != "failed" {
		t.Errorf("status = %q, want failed (web page rejected)", it.Status)
	}
	if it.Path != "" {
		if _, err := os.Stat(it.Path); err == nil {
			t.Errorf("a rejected web page was saved to disk at %q", it.Path)
		}
	}
}

// TestDirectEmptyURLRejected: an empty download URL errors immediately with
// the needs-a-key hint.
func TestDirectEmptyURLRejected(t *testing.T) {
	d := newTestDirect(t)
	if _, err := d.Add(context.Background(), "", "No URL"); err == nil ||
		!strings.Contains(err.Error(), "membership") {
		t.Errorf("Add(\"\") = %v, want a needs-key error", err)
	}
}

// TestDirectProtocolRouting: the config reports the direct protocol, and the
// grab path routes direct releases to it.
func TestDirectProtocolRouting(t *testing.T) {
	cfg := &ClientConfig{Type: TypeDirect}
	if cfg.Protocol() != ProtocolDirect {
		t.Errorf("Protocol() = %q, want direct", cfg.Protocol())
	}
	c, err := New(cfg)
	if err != nil || c == nil {
		t.Fatalf("New(direct) = %v", err)
	}
}
