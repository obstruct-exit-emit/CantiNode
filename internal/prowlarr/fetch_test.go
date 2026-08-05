package prowlarr

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchContentDirectMagnetURI(t *testing.T) {
	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{Title: "X", Protocol: ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:ABCDEF1234567890"}

	got, err := c.FetchContent(t.Context(), rel)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if got.Kind != KindMagnet || got.MagnetURI != rel.MagnetURL {
		t.Errorf("got = %+v", got)
	}
}

func TestFetchContentFollowsRedirectToMagnet(t *testing.T) {
	const magnetURI = "magnet:?xt=urn:btih:ABCDEF1234567890&dn=Some+Release"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", magnetURI)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{Title: "X", Protocol: ProtocolTorrent, MagnetURL: srv.URL + "/1/download"}

	got, err := c.FetchContent(t.Context(), rel)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if got.Kind != KindMagnet || got.MagnetURI != magnetURI {
		t.Errorf("got = %+v, want magnet %q", got, magnetURI)
	}
}

func TestFetchContentDownloadsFileBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake torrent file bytes"))
	}))
	defer srv.Close()

	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{Title: "My Release", Protocol: ProtocolTorrent, DownloadURL: srv.URL + "/1/download"}

	got, err := c.FetchContent(t.Context(), rel)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if got.Kind != KindFile {
		t.Fatalf("Kind = %q, want file", got.Kind)
	}
	if string(got.Data) != "fake torrent file bytes" {
		t.Errorf("Data = %q", got.Data)
	}
	if got.Filename != "My Release.torrent" {
		t.Errorf("Filename = %q", got.Filename)
	}
}

func TestFetchContentFollowsMultipleHTTPRedirectsBeforeFile(t *testing.T) {
	var hops int
	mux := http.NewServeMux()
	mux.HandleFunc("/hop1", func(w http.ResponseWriter, r *http.Request) {
		hops++
		w.Header().Set("Location", "/hop2")
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/hop2", func(w http.ResponseWriter, r *http.Request) {
		hops++
		w.Write([]byte("nzb bytes"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{Title: "X", Protocol: ProtocolUsenet, DownloadURL: srv.URL + "/hop1"}

	got, err := c.FetchContent(t.Context(), rel)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if string(got.Data) != "nzb bytes" {
		t.Errorf("Data = %q", got.Data)
	}
	if hops != 2 {
		t.Errorf("hops = %d, want 2", hops)
	}
}

func TestFetchContentPrefersMagnetURLOverDownloadURL(t *testing.T) {
	var downloadURLHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadURLHit = true
		w.Write([]byte("should not be fetched"))
	}))
	defer srv.Close()

	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{
		Title:       "X",
		MagnetURL:   "magnet:?xt=urn:btih:ABC",
		DownloadURL: srv.URL + "/1/download",
	}

	got, err := c.FetchContent(t.Context(), rel)
	if err != nil {
		t.Fatalf("FetchContent: %v", err)
	}
	if got.Kind != KindMagnet {
		t.Errorf("Kind = %q, want magnet (magnetUrl should be preferred)", got.Kind)
	}
	if downloadURLHit {
		t.Error("downloadUrl should not have been fetched when magnetUrl is a direct magnet URI")
	}
}

func TestFetchContentNoLinksAtAll(t *testing.T) {
	c := NewClient("http://unused.invalid", "key", "ua")
	if _, err := c.FetchContent(t.Context(), Release{Title: "X"}); err == nil {
		t.Error("expected an error when the release has neither magnetUrl nor downloadUrl")
	}
}

func TestFetchContentNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient("http://unused.invalid", "key", "ua")
	rel := Release{Title: "X", DownloadURL: srv.URL + "/gone"}
	if _, err := c.FetchContent(t.Context(), rel); err == nil {
		t.Error("expected an error on 404")
	}
}
