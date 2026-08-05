package prowlarr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleReleaseJSON = `{
	"guid": "indexer1-guid-1",
	"title": "Boards of Canada - Geogaddi [FLAC]",
	"size": 314572800,
	"indexerId": 1,
	"indexer": "SomeMusicIndexer",
	"publishDate": "2024-01-15T00:00:00Z",
	"downloadUrl": "%s/download1",
	"magnetUrl": "%s/magnet1",
	"infoUrl": "%s/info1",
	"protocol": "torrent",
	"seeders": 42,
	"leechers": 3
}`

func TestSearch(t *testing.T) {
	var gotPath, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-Api-Key")
		q := r.URL.Query()
		if q.Get("query") != "Boards of Canada Geogaddi" {
			t.Errorf("query = %q", q.Get("query"))
		}
		if q.Get("categories") != "3000" {
			t.Errorf("categories = %q, want 3000 (default Music category)", q.Get("categories"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[" + `{"guid":"g1","title":"Release One","protocol":"torrent"}` + "]"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-prowlarr-key", "cantinode-test/0.1")
	releases, err := c.Search(t.Context(), "Boards of Canada Geogaddi")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/api/v1/search" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAPIKey != "test-prowlarr-key" {
		t.Errorf("X-Api-Key = %q", gotAPIKey)
	}
	if len(releases) != 1 || releases[0].Title != "Release One" {
		t.Errorf("releases = %+v", releases)
	}
	if releases[0].Protocol != ProtocolTorrent {
		t.Errorf("Protocol = %q, want torrent", releases[0].Protocol)
	}
}

func TestSearchCustomCategories(t *testing.T) {
	var gotCategories []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCategories = r.URL.Query()["categories"]
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "ua")
	if _, err := c.Search(t.Context(), "q", 3010, 3040); err != nil {
		t.Fatal(err)
	}
	if len(gotCategories) != 2 || gotCategories[0] != "3010" || gotCategories[1] != "3040" {
		t.Errorf("categories = %v, want [3010 3040]", gotCategories)
	}
}

func TestSearchNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("bad api key"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "wrong-key", "ua")
	if _, err := c.Search(t.Context(), "q"); err == nil {
		t.Error("expected an error on 401")
	}
}

func TestProtocolUnmarshalsStringOrInt(t *testing.T) {
	cases := []struct {
		json string
		want Protocol
	}{
		{`"torrent"`, ProtocolTorrent},
		{`"Torrent"`, ProtocolTorrent},
		{`"usenet"`, ProtocolUsenet},
		{`2`, ProtocolTorrent},
		{`1`, ProtocolUsenet},
		{`0`, ProtocolUnknown},
		{`"nonsense"`, ProtocolUnknown},
	}
	for _, tc := range cases {
		var p Protocol
		if err := p.UnmarshalJSON([]byte(tc.json)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", tc.json, err)
			continue
		}
		if p != tc.want {
			t.Errorf("UnmarshalJSON(%s) = %q, want %q", tc.json, p, tc.want)
		}
	}
}

func TestReleaseFileName(t *testing.T) {
	torrent := Release{Title: "Some Release", Protocol: ProtocolTorrent}
	if got := torrent.FileName(); got != "Some Release.torrent" {
		t.Errorf("torrent FileName = %q", got)
	}
	usenet := Release{Title: "Some Release", Protocol: ProtocolUsenet}
	if got := usenet.FileName(); got != "Some Release.nzb" {
		t.Errorf("usenet FileName = %q", got)
	}
}

func TestSearchDecodesFullRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := strings.ReplaceAll(sampleReleaseJSON, "%s", "http://example.invalid")
		w.Write([]byte("[" + body + "]"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "key", "ua")
	releases, err := c.Search(t.Context(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 {
		t.Fatalf("len(releases) = %d, want 1", len(releases))
	}
	r := releases[0]
	if r.Indexer != "SomeMusicIndexer" || r.IndexerID != 1 {
		t.Errorf("Indexer=%q IndexerID=%d", r.Indexer, r.IndexerID)
	}
	if r.Seeders == nil || *r.Seeders != 42 {
		t.Errorf("Seeders = %v, want 42", r.Seeders)
	}
	if r.Size != 314572800 {
		t.Errorf("Size = %d", r.Size)
	}
}
