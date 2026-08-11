package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/database"
)

// mockQbit fakes qBittorrent's Web API v2 with session-cookie auth.
func mockQbit(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Release download endpoints resolve() fetches — no auth, like an indexer.
		switch r.URL.Path {
		case "/dl/magnet": // magnet-only indexer redirects to the magnet
			w.Header().Set("Location", "magnet:?xt=urn:btih:redirected")
			w.WriteHeader(http.StatusFound)
			return
		case "/dl/torrent": // indexer serves a .torrent file
			w.Header().Set("Content-Type", "application/x-bittorrent")
			w.Write([]byte("d8:announce9:udp://x:04:infod4:name9:Mort.epub6:lengthi123eee"))
			return
		}
		switch r.URL.Path {
		case "/api/v2/auth/login":
			r.ParseForm()
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
				w.Write([]byte("Fails."))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "mock-session", Path: "/"})
			w.Write([]byte("Ok."))
		default:
			if c, err := r.Cookie("SID"); err != nil || c.Value != "mock-session" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			switch r.URL.Path {
			case "/api/v2/app/version":
				w.Write([]byte("v5.0.0"))
			case "/api/v2/torrents/createCategory":
				w.WriteHeader(http.StatusConflict) // already exists
			case "/api/v2/torrents/add":
				// Either a form (urls=magnet) or a multipart .torrent upload.
				if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						w.Write([]byte("Fails."))
						return
					}
					f, _, err := r.FormFile("torrents")
					if err != nil || r.FormValue("category") != "cantinode" {
						w.Write([]byte("Fails."))
						return
					}
					f.Close()
					w.Write([]byte("Ok."))
					return
				}
				r.ParseForm()
				if r.Form.Get("category") != "cantinode" || r.Form.Get("urls") == "" {
					w.Write([]byte("Fails."))
					return
				}
				w.Write([]byte("Ok."))
			case "/api/v2/torrents/info":
				if r.URL.Query().Get("category") != "cantinode" {
					w.Write([]byte("[]"))
					return
				}
				w.Write([]byte(`[
					{"hash":"aaa","name":"Mort.epub","state":"downloading","progress":0.42,"content_path":"","save_path":"/dl","category":"cantinode"},
					{"hash":"bbb","name":"Guards.epub","state":"stalledUP","progress":1,"content_path":"/dl/Guards.epub","category":"cantinode"},
					{"hash":"ccc","name":"Bad.epub","state":"error","progress":0.1,"category":"cantinode"},
					{"hash":"ddd","name":"SomeMovie.mkv","state":"stalledUP","progress":1,"content_path":"/dl/SomeMovie.mkv","category":"radarr"}
				]`))
			case "/api/v2/torrents/delete":
				r.ParseForm()
				if r.Form.Get("hashes") == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Write([]byte(""))
			default:
				http.NotFound(w, r)
			}
		}
	}))
}

func qbitConfig(host string) *ClientConfig {
	return &ClientConfig{
		Name: "qbit", Type: TypeQBittorrent, Host: host,
		Username: "admin", Password: "secret", Category: "cantinode",
		Enabled: true, Priority: 1,
	}
}

func TestQBittorrent(t *testing.T) {
	srv := mockQbit(t)
	defer srv.Close()
	ctx := context.Background()

	c, err := New(qbitConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	// Wrong password surfaces as a login failure.
	bad := qbitConfig(srv.URL)
	bad.Password = "nope"
	badClient, _ := New(bad)
	if err := badClient.Test(ctx); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("bad credentials: err = %v", err)
	}

	if _, err := c.Add(ctx, "magnet:?xt=urn:btih:abc", "Mort"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A non-magnet URL that redirects to a magnet is resolved on our side (the
	// client often can't reach the LAN indexer) and the magnet is added.
	if _, err := c.Add(ctx, srv.URL+"/dl/magnet", "Mort"); err != nil {
		t.Fatalf("Add via magnet redirect: %v", err)
	}
	// A .torrent file URL is fetched and uploaded via multipart.
	if _, err := c.Add(ctx, srv.URL+"/dl/torrent", "Mort"); err != nil {
		t.Fatalf("Add via torrent file: %v", err)
	}

	items, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The server returns a 4th torrent (category "radarr") despite the
	// category=cantinode query param — some qBittorrent-compatible bridges
	// (debrid services) don't honor it. It must be filtered client-side.
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 (radarr-categorized torrent excluded)", items)
	}
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if _, ok := byID["ddd"]; ok {
		t.Error("torrent from another app's category (radarr) leaked into our list")
	}
	if byID["aaa"].Status != "downloading" || byID["aaa"].Progress != 0.42 {
		t.Errorf("downloading item = %+v", byID["aaa"])
	}
	if byID["bbb"].Status != "completed" || byID["bbb"].Path != "/dl/Guards.epub" {
		t.Errorf("completed item = %+v", byID["bbb"])
	}
	if byID["ccc"].Status != "failed" {
		t.Errorf("failed item = %+v", byID["ccc"])
	}

	if err := c.Remove(ctx, "aaa", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// mockSab fakes SABnzbd's single-endpoint API plus an NZB download path (Add
// fetches the NZB and uploads it via addfile).
func mockSab(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/get/") {
			w.Header().Set("Content-Type", "application/x-nzb")
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file subject="Mort"></file></nzb>`))
			return
		}
		q := r.URL.Query()
		if q.Get("apikey") != "sab-key" {
			w.Write([]byte(`{"status": false, "error": "API Key Incorrect"}`))
			return
		}
		switch q.Get("mode") {
		case "version":
			w.Write([]byte(`{"version": "4.3.2"}`))
		case "addfile":
			// The NZB content arrives as a multipart file; the name comes from
			// nzbname (not the URL), so the job is identifiable.
			if q.Get("cat") != "cantinode" || q.Get("nzbname") == "" {
				w.Write([]byte(`{"status": false, "error": "bad request"}`))
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				w.Write([]byte(`{"status": false, "error": "no file"}`))
				return
			}
			f, _, err := r.FormFile("name")
			if err != nil {
				w.Write([]byte(`{"status": false, "error": "no file field"}`))
				return
			}
			f.Close()
			w.Write([]byte(`{"status": true, "nzo_ids": ["SABnzbd_nzo_x1"]}`))
		case "addurl":
			if q.Get("cat") != "cantinode" || q.Get("name") == "" {
				w.Write([]byte(`{"status": false, "error": "bad request"}`))
				return
			}
			w.Write([]byte(`{"status": true, "nzo_ids": ["SABnzbd_nzo_x1"]}`))
		case "queue":
			// SABnzbd's queue slots name the category field "cat" — unlike
			// history, which calls it "category" (see sabSlot). A radarr-
			// categorized slot is included to confirm the client-side filter
			// still excludes it even though the request asked for cat=cantinode
			// — some SABnzbd-compatible bridges don't honor that server-side.
			w.Write([]byte(`{"queue": {"slots": [
				{"nzo_id": "SABnzbd_nzo_q1", "filename": "Mort", "status": "Downloading", "percentage": "34", "cat": "cantinode"},
				{"nzo_id": "SABnzbd_nzo_q2", "filename": "SomeShow", "status": "Downloading", "percentage": "10", "cat": "radarr"}
			]}}`))
		case "history":
			// A radarr-categorized slot is included even though the request
			// asked for category=cantinode — some SABnzbd-compatible bridges
			// (debrid services) don't honor that filter server-side.
			w.Write([]byte(`{"history": {"slots": [
				{"nzo_id": "SABnzbd_nzo_h1", "name": "Guards", "status": "Completed", "storage": "/complete/Guards", "category": "cantinode"},
				{"nzo_id": "SABnzbd_nzo_h2", "name": "Broken", "status": "Failed", "fail_message": "crc", "category": "cantinode"},
				{"nzo_id": "SABnzbd_nzo_h3", "name": "SomeMovie", "status": "Completed", "storage": "/complete/SomeMovie", "category": "radarr"}
			]}}`))
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
}

func sabConfig(host string) *ClientConfig {
	return &ClientConfig{
		Name: "sab", Type: TypeSABnzbd, Host: host,
		APIKey: "sab-key", Category: "cantinode", Enabled: true, Priority: 1,
	}
}

func TestSABnzbd(t *testing.T) {
	srv := mockSab(t)
	defer srv.Close()
	ctx := context.Background()

	c, err := New(sabConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	bad := sabConfig(srv.URL)
	bad.APIKey = "wrong"
	badClient, _ := New(bad)
	if err := badClient.Test(ctx); err == nil || !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Fatalf("bad key: err = %v", err)
	}

	// Add fetches the NZB from the URL and uploads it via addfile.
	id, err := c.Add(ctx, srv.URL+"/get/abc.nzb", "Mort")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "SABnzbd_nzo_x1" {
		t.Errorf("nzo id = %q", id)
	}

	// A non-NZB URL (unfetchable/HTML) falls back to addurl and still works.
	if id, err = c.Add(ctx, srv.URL+"/api?mode=version", "Mort"); err != nil {
		t.Fatalf("Add (addurl fallback): %v", err)
	}
	if id != "SABnzbd_nzo_x1" {
		t.Errorf("fallback nzo id = %q", id)
	}

	items, err := c.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v, want 3 (radarr-categorized slots excluded)", items)
	}
	byID := map[string]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if _, ok := byID["SABnzbd_nzo_h3"]; ok {
		t.Error("download from another app's category (radarr) leaked into our list")
	}
	if _, ok := byID["SABnzbd_nzo_q2"]; ok {
		t.Error("another app's in-progress download (radarr) leaked into our queue")
	}
	// Regression: the queue's still-downloading item must appear at all — a
	// prior bug read the wrong JSON field for the queue's category (SABnzbd
	// calls it "cat" there, "category" in history) and so filtered out every
	// real in-progress usenet download, leaving Activity showing nothing
	// until the download finished and moved into history.
	if byID["SABnzbd_nzo_q1"].Status != "downloading" || byID["SABnzbd_nzo_q1"].Progress != 0.34 {
		t.Errorf("queue item = %+v", byID["SABnzbd_nzo_q1"])
	}
	if byID["SABnzbd_nzo_h1"].Status != "completed" || byID["SABnzbd_nzo_h1"].Path != "/complete/Guards" {
		t.Errorf("history item = %+v", byID["SABnzbd_nzo_h1"])
	}
	if byID["SABnzbd_nzo_h2"].Status != "failed" {
		t.Errorf("failed item = %+v", byID["SABnzbd_nzo_h2"])
	}

	if err := c.Remove(ctx, "SABnzbd_nzo_q1", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// TestSABnzbdNeverLeaksAPIKey: SABnzbd's apikey rides in every request's
// query string — a connection failure must not carry it into the returned
// error, since that string can end up in the Activity page and, for
// background polls, the health check and logs.
func TestSABnzbdNeverLeaksAPIKey(t *testing.T) {
	cfg := sabConfig("http://127.0.0.1:1")
	cfg.APIKey = "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Test(context.Background()); err == nil {
		t.Fatal("expected Test to fail against an unreachable host")
	} else if strings.Contains(err.Error(), cfg.APIKey) {
		t.Fatalf("API key leaked into error: %q", err)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewService(NewStore(db))
}

// TestQueueServesStaleSnapshotWhileRevalidating is the regression test for
// the actual bug: some SABnzbd-compatible bridges (a debrid service in
// particular) answer several real seconds slower than a plain download
// client — not a connection-warmup cost, genuine per-request latency — so a
// naive "block on a live sweep whenever the cache expires" design made the
// Activity page visibly hang on every reload past queueCacheTTL. Once a
// snapshot exists at all, a stale hit must return immediately with the old
// snapshot while a slow sweep refreshes it in the background; only the very
// first call (nothing cached yet) is allowed to block.
func TestQueueServesStaleSnapshotWhileRevalidating(t *testing.T) {
	var (
		callCount int32
		release   = make(chan struct{})
	)
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("mode") {
		case "queue":
			w.Write([]byte(`{"queue": {"slots": []}}`))
		case "history":
			n := atomic.AddInt32(&callCount, 1)
			if n == 1 {
				// First sweep (the blocking one, cache empty): answers
				// immediately with "v1" so Queue's first call doesn't hang
				// the test itself.
				w.Write([]byte(`{"history": {"slots": [
					{"nzo_id": "v1", "name": "First", "status": "Completed", "storage": "/complete/First", "category": "cantinode"}
				]}}`))
				return
			}
			// Second sweep (the background refresh): blocks until the test
			// explicitly releases it, simulating the bridge's real
			// multi-second-even-when-warm latency.
			<-release
			w.Write([]byte(`{"history": {"slots": [
				{"nzo_id": "v2", "name": "Second", "status": "Completed", "storage": "/complete/Second", "category": "cantinode"}
			]}}`))
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
	defer sab.Close()

	svc := newTestService(t)
	if err := svc.Store().Add(sabConfig(sab.URL)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// First call: nothing cached yet, must block on the (fast) first sweep.
	items, _, err := svc.Queue(ctx)
	if err != nil {
		t.Fatalf("first Queue: %v", err)
	}
	if len(items) != 1 || items[0].Title != "First" {
		t.Fatalf("first Queue items = %+v, want [First]", items)
	}

	// Force the cache stale, then call again while the second sweep is
	// deliberately blocked — this must return the OLD snapshot immediately,
	// not hang waiting for the slow one.
	svc.mu.Lock()
	svc.cachedAt = time.Now().Add(-queueCacheTTL - time.Second)
	svc.mu.Unlock()

	done := make(chan struct{})
	var staleItems []Item
	go func() {
		staleItems, _, _ = svc.Queue(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Queue blocked on the slow background sweep instead of serving the stale snapshot")
	}
	if len(staleItems) != 1 || staleItems[0].Title != "First" {
		t.Fatalf("stale-serve items = %+v, want [First] (the old snapshot)", staleItems)
	}

	// Let the background sweep finish, then confirm the cache picked up the
	// fresh data for the next caller.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.mu.Lock()
		refreshed := len(svc.cached) == 1 && svc.cached[0].Title == "Second"
		svc.mu.Unlock()
		if refreshed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background sweep never refreshed the cache with the new snapshot")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServiceGrabAndQueue(t *testing.T) {
	qbit := mockQbit(t)
	defer qbit.Close()
	sab := mockSab(t)
	defer sab.Close()
	ctx := context.Background()

	svc := newTestService(t)
	qc := qbitConfig(qbit.URL)
	sc := sabConfig(sab.URL)
	if err := svc.Store().Add(qc); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store().Add(sc); err != nil {
		t.Fatal(err)
	}

	// Grabs route by protocol.
	torrentGrab, err := svc.Grab(ctx, ProtocolTorrent, "magnet:?xt=urn:btih:abc", "Mort")
	if err != nil {
		t.Fatalf("torrent grab: %v", err)
	}
	if torrentGrab.Client != "qbit" {
		t.Errorf("torrent grab = %+v", torrentGrab)
	}
	usenetGrab, err := svc.Grab(ctx, ProtocolUsenet, sab.URL+"/get/abc.nzb", "Mort")
	if err != nil {
		t.Fatalf("usenet grab: %v", err)
	}
	if usenetGrab.Client != "sab" || usenetGrab.ID == "" {
		t.Errorf("usenet grab = %+v", usenetGrab)
	}

	// Queue aggregates both clients.
	items, errs, err := svc.Queue(ctx)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("queue errs = %v", errs)
	}
	if len(items) != 6 { // 3 qbit + 3 sab
		t.Fatalf("%d items, want 6: %+v", len(items), items)
	}

	// No client for a protocol → ErrNoClient.
	qc.Enabled = false
	if err := svc.Store().Update(qc); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Grab(ctx, ProtocolTorrent, "magnet:x", "y"); err != ErrNoClient {
		t.Errorf("grab without client: err = %v, want ErrNoClient", err)
	}
}

func TestStoreCRUD(t *testing.T) {
	svc := newTestService(t)
	s := svc.Store()

	c := qbitConfig("http://localhost:8080")
	if err := s.Add(c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("id not set")
	}
	dup := qbitConfig("http://other")
	if err := s.Add(dup); err == nil {
		t.Error("duplicate name should fail")
	}

	c.Category = "books"
	if err := s.Update(c); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(c.ID)
	if got.Category != "books" {
		t.Errorf("updated = %+v", got)
	}

	if err := s.Delete(c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete(c.ID); err != ErrNotFound {
		t.Errorf("double delete: %v", err)
	}
}
