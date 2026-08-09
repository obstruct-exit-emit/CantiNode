package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/librinode/librinode/internal/database"
)

const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <server title="Mock Indexer"/>
  <categories>
    <category id="7000" name="Books">
      <subcat id="7020" name="Ebook"/>
    </category>
  </categories>
</caps>`

const newznabSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
<channel>
  <item>
    <title>Terry Pratchett - Mort (1987) Retail EPUB</title>
    <guid>https://mock/details/abc123</guid>
    <link>https://mock/getnzb/abc123.nzb</link>
    <comments>https://mock/details/abc123#comments</comments>
    <pubDate>Wed, 01 Jan 2025 12:00:00 +0000</pubDate>
    <enclosure url="https://mock/getnzb/abc123.nzb" length="1048576" type="application/x-nzb"/>
    <newznab:attr name="category" value="7000"/>
    <newznab:attr name="category" value="7020"/>
    <newznab:attr name="size" value="1048576"/>
  </item>
  <item>
    <title>Terry Pratchett - Guards Guards (1989) MOBI</title>
    <guid>https://mock/details/def456</guid>
    <link>https://mock/getnzb/def456.nzb</link>
    <pubDate>Thu, 02 Jan 2025 12:00:00 +0000</pubDate>
    <newznab:attr name="size" value="2097152"/>
  </item>
</channel>
</rss>`

const torznabSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <item>
    <title>Terry Pratchett - Mort [epub]</title>
    <guid>https://mock/torrent/999</guid>
    <link>https://mock/dl/999.torrent</link>
    <pubDate>Fri, 03 Jan 2025 12:00:00 +0000</pubDate>
    <torznab:attr name="category" value="7020"/>
    <torznab:attr name="size" value="524288"/>
    <torznab:attr name="seeders" value="12"/>
    <torznab:attr name="peers" value="3"/>
  </item>
</channel>
</rss>`

const errorXML = `<?xml version="1.0" encoding="UTF-8"?>
<error code="100" description="Incorrect user credentials"/>`

// mockIndexer serves caps and search responses, checking the query shape.
func mockIndexer(t *testing.T, searchXML string, wantAPIKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") {
			t.Errorf("expected /api path, got %s", r.URL.Path)
		}
		if wantAPIKey != "" && r.URL.Query().Get("apikey") != wantAPIKey {
			w.Write([]byte(errorXML))
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Query().Get("t") {
		case "caps":
			w.Write([]byte(capsXML))
		case "search":
			if got := r.URL.Query().Get("cat"); got != "7000,7020" {
				t.Errorf("cat = %q, want 7000,7020", got)
			}
			w.Write([]byte(searchXML))
		default:
			t.Errorf("unexpected t=%s", r.URL.Query().Get("t"))
		}
	}))
}

func testIndexer(srv *httptest.Server, typ string) *Indexer {
	return &Indexer{
		ID: 1, Name: "mock", Type: typ, BaseURL: srv.URL,
		APIKey: "s3cret", Categories: "7000,7020", Enabled: true, Priority: 25,
	}
}

func TestClientTestAndAuth(t *testing.T) {
	srv := mockIndexer(t, newznabSearchXML, "s3cret")
	defer srv.Close()
	c := NewClient()

	if err := c.Test(context.Background(), testIndexer(srv, TypeNewznab)); err != nil {
		t.Fatalf("Test: %v", err)
	}

	bad := testIndexer(srv, TypeNewznab)
	bad.APIKey = "wrong"
	err := c.Test(context.Background(), bad)
	if err == nil || !strings.Contains(err.Error(), "Incorrect user credentials") {
		t.Fatalf("bad key: err = %v, want credentials error", err)
	}
}

func TestClientSearchNewznab(t *testing.T) {
	srv := mockIndexer(t, newznabSearchXML, "s3cret")
	defer srv.Close()

	releases, err := NewClient().Search(context.Background(), testIndexer(srv, TypeNewznab), "mort", "7000,7020")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("got %d releases, want 2", len(releases))
	}

	r := releases[0]
	if r.Title != "Terry Pratchett - Mort (1987) Retail EPUB" {
		t.Errorf("title = %q", r.Title)
	}
	if r.Protocol != ProtocolUsenet || r.Seeders != -1 {
		t.Errorf("usenet fields: protocol=%s seeders=%d", r.Protocol, r.Seeders)
	}
	if r.Size != 1048576 {
		t.Errorf("size = %d", r.Size)
	}
	if len(r.Categories) != 2 || r.Categories[1] != 7020 {
		t.Errorf("categories = %v", r.Categories)
	}
	if r.PublishDate != "2025-01-01T12:00:00Z" {
		t.Errorf("pubDate = %q", r.PublishDate)
	}
	if r.DownloadURL != "https://mock/getnzb/abc123.nzb" {
		t.Errorf("download = %q", r.DownloadURL)
	}
	// Second item has no enclosure: size comes from the attr.
	if releases[1].Size != 2097152 {
		t.Errorf("attr size = %d", releases[1].Size)
	}
}

func TestClientSearchTorznab(t *testing.T) {
	srv := mockIndexer(t, torznabSearchXML, "s3cret")
	defer srv.Close()

	releases, err := NewClient().Search(context.Background(), testIndexer(srv, TypeTorznab), "mort", "7000,7020")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1", len(releases))
	}
	r := releases[0]
	if r.Protocol != ProtocolTorrent || r.Seeders != 12 || r.Peers != 3 {
		t.Errorf("torrent fields: %+v", r)
	}
	if r.Size != 524288 {
		t.Errorf("size = %d", r.Size)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestStoreCRUD(t *testing.T) {
	s := newTestStore(t)

	i := &Indexer{Name: "usenet-1", Type: TypeNewznab, BaseURL: "https://a.example",
		APIKey: "k", Categories: "7000,7020", Enabled: true, Priority: 25}
	if err := s.Add(i); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if i.ID == 0 || i.AddedAt == "" {
		t.Fatalf("Add did not populate row: %+v", i)
	}

	// Duplicate names rejected.
	dup := *i
	dup.ID = 0
	if err := s.Add(&dup); err == nil {
		t.Fatal("duplicate name should fail")
	}

	i.Enabled = false
	i.Priority = 10
	if err := s.Update(i); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(i.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled || got.Priority != 10 {
		t.Errorf("update not persisted: %+v", got)
	}

	all, _ := s.List()
	enabled, _ := s.ListEnabled()
	if len(all) != 1 || len(enabled) != 0 {
		t.Errorf("List/ListEnabled = %d/%d, want 1/0", len(all), len(enabled))
	}

	if err := s.Delete(i.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete(i.ID); err != ErrNotFound {
		t.Errorf("double delete: err = %v, want ErrNotFound", err)
	}
}

func TestSearchAllMergesAndReportsFailures(t *testing.T) {
	good := mockIndexer(t, torznabSearchXML, "")
	defer good.Close()
	good2 := mockIndexer(t, newznabSearchXML, "")
	defer good2.Close()

	store := newTestStore(t)
	svc := NewService(store)

	for _, ind := range []*Indexer{
		{Name: "torrents", Type: TypeTorznab, BaseURL: good.URL, Categories: "7000,7020", Enabled: true, Priority: 25},
		{Name: "usenet", Type: TypeNewznab, BaseURL: good2.URL, Categories: "7000,7020", Enabled: true, Priority: 25},
		{Name: "dead", Type: TypeNewznab, BaseURL: "http://127.0.0.1:1", Categories: "7000,7020", Enabled: true, Priority: 25},
		{Name: "disabled", Type: TypeNewznab, BaseURL: "http://127.0.0.1:1", Categories: "7000,7020", Enabled: false, Priority: 25},
	} {
		if err := store.Add(ind); err != nil {
			t.Fatal(err)
		}
	}

	releases, errs, err := svc.SearchAll(context.Background(), "mort", "", "ebook")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("got %d releases, want 3 (2 usenet + 1 torrent)", len(releases))
	}
	// Torrent with seeders sorts first.
	if releases[0].Seeders != 12 {
		t.Errorf("first release = %+v, want the seeded torrent", releases[0])
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "dead:") {
		t.Errorf("errs = %v, want one from 'dead'", errs)
	}
}

// TestSearchAllNeverLeaksAPIKey: an indexer's apikey rides in the request
// URL's query string (Newznab/Torznab convention) — a connection failure to
// it must not carry that key into the error string SearchAll returns, since
// that string ends up in the UI's search-error notice and, for automatic
// sweeps, the health/log output. A real-looking secret is used so a passing
// test can't hide a redaction bug behind a value too short to notice.
func TestSearchAllNeverLeaksAPIKey(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)

	secret := "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
	if err := store.Add(&Indexer{
		Name: "dead", Type: TypeNewznab, BaseURL: "http://127.0.0.1:1",
		APIKey: secret, Categories: "7000,7020", Enabled: true, Priority: 25,
	}); err != nil {
		t.Fatal(err)
	}

	_, errs, err := svc.SearchAll(context.Background(), "mort", "", "ebook")
	if err != nil {
		t.Fatalf("SearchAll: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one", errs)
	}
	if strings.Contains(errs[0], secret) {
		t.Fatalf("API key leaked into search error: %q", errs[0])
	}
	if !strings.Contains(errs[0], "REDACTED") {
		t.Errorf("expected a REDACTED marker in %q", errs[0])
	}
}

func TestSearchAllBacksOffFailingIndexer(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	now := time.Now()
	svc.now = func() time.Time { return now }

	dead := &Indexer{Name: "dead", Type: TypeNewznab, BaseURL: "http://127.0.0.1:1",
		Categories: "7000,7020", Enabled: true, Priority: 25}
	if err := store.Add(dead); err != nil {
		t.Fatal(err)
	}

	// The first restAfter failures are tolerated: each sweep still retries and
	// reports a real failure, never a resting notice — a transient blip on a
	// scraped source shouldn't hide it.
	for i := 0; i < restAfter; i++ {
		_, errs, err := svc.SearchAll(context.Background(), "mort", "", "ebook")
		if err != nil {
			t.Fatal(err)
		}
		if len(errs) != 1 || strings.Contains(errs[0], "resting") {
			t.Fatalf("failure %d errs = %v, want a real failure (not resting yet)", i+1, errs)
		}
	}
	// Now it rests — backoffBase after the restAfter-th consecutive failure.
	if until, ok := svc.resting(dead.ID); !ok || until.Sub(now) != backoffBase {
		t.Fatalf("should rest %v after %d failures, got %v (resting=%v)", backoffBase, restAfter, until.Sub(now), ok)
	}

	// A sweep inside the rest window is skipped with a resting notice.
	_, errs, err := svc.SearchAll(context.Background(), "mort", "", "ebook")
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || !strings.Contains(errs[0], "resting") {
		t.Fatalf("resting sweep errs = %v, want a resting notice", errs)
	}

	// Past the rest: retried again — another failure doubles the rest.
	now = now.Add(backoffBase + time.Minute)
	_, errs, _ = svc.SearchAll(context.Background(), "mort", "", "ebook")
	if len(errs) != 1 || strings.Contains(errs[0], "resting") {
		t.Fatalf("post-rest sweep errs = %v, want a real failure", errs)
	}
	if until, ok := svc.resting(dead.ID); !ok || until.Sub(now) != 2*backoffBase {
		t.Fatalf("next failure should rest %v, got %v (resting=%v)", 2*backoffBase, until.Sub(now), ok)
	}

	// A success clears the slate. Point the indexer at a working server.
	good := mockIndexer(t, newznabSearchXML, "")
	defer good.Close()
	dead.BaseURL = good.URL
	if err := store.Update(dead); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Minute)
	_, errs, _ = svc.SearchAll(context.Background(), "mort", "", "ebook")
	if len(errs) != 0 {
		t.Fatalf("recovered sweep errs = %v, want none", errs)
	}
	if _, ok := svc.resting(dead.ID); ok {
		t.Fatal("success should clear the backoff")
	}
}
