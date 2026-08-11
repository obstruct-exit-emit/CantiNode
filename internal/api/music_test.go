package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockTorznabIndexer serves a fixed torznab search response regardless of
// query — good enough for exercising the scoring/filtering pipeline
// downstream of the search itself, which is what these tests are about.
func mockTorznabIndexer(t *testing.T, searchXML string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Query().Get("t") {
		case "caps":
			w.Write([]byte(`<?xml version="1.0"?><caps></caps>`))
		case "search":
			w.Write([]byte(searchXML))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const musicSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <item>
    <title>Boards of Canada - Geogaddi FLAC</title>
    <guid>https://mock/torrent/good</guid>
    <link>https://mock/dl/good.torrent</link>
    <torznab:attr name="size" value="400000000"/>
    <torznab:attr name="seeders" value="20"/>
    <torznab:attr name="peers" value="5"/>
  </item>
  <item>
    <title>Boards of Canada - Geogaddi FLAC Setup.exe</title>
    <guid>https://mock/torrent/spam</guid>
    <link>https://mock/dl/spam.torrent</link>
    <torznab:attr name="size" value="400000000"/>
    <torznab:attr name="seeders" value="20"/>
    <torznab:attr name="peers" value="5"/>
  </item>
  <item>
    <title>Boards of Canada - Geogaddi FLAC Dead Torrent</title>
    <guid>https://mock/torrent/dead</guid>
    <link>https://mock/dl/dead.torrent</link>
    <torznab:attr name="size" value="400000000"/>
    <torznab:attr name="seeders" value="0"/>
    <torznab:attr name="peers" value="0"/>
  </item>
  <item>
    <title>Boards of Canada - Geogaddi Unknown Format</title>
    <guid>https://mock/torrent/unknown</guid>
    <link>https://mock/dl/unknown.torrent</link>
    <torznab:attr name="size" value="400000000"/>
    <torznab:attr name="seeders" value="20"/>
    <torznab:attr name="peers" value="5"/>
  </item>
</channel>
</rss>`

// seedWantedAlbum inserts a monitored artist and one wanted-album row
// directly (faster and more direct than round-tripping through MusicBrainz
// via the real API), returning the wanted_albums row's own id.
func seedWantedAlbum(t *testing.T, a *testAPI) int64 {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO artists (mbid, name, sort_name, is_monitored) VALUES (?, ?, ?, 1)`,
		"artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	artistID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO wanted_albums (artist_id, release_group_mbid, title, status) VALUES (?, ?, ?, 'wanted')`,
		artistID, "rg-geogaddi", "Geogaddi")
	if err != nil {
		t.Fatalf("insert wanted album: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestSearchWantedMusicAlbumScoresAndFilters is the regression test for a
// real bug: handleSearchWantedMusicAlbum used to return SearchAll's raw,
// unranked results straight to the caller — internal/release's scoring
// engine (quality profiles, spam rejection, dead-torrent rejection) had no
// caller anywhere in the codebase at all. It now returns every candidate,
// scored (approved and rejected alike, blocklisted ones dropped outright)
// — like ReleaseBrowser, the UI decides what to show, not the API — so a
// release naming an executable and a dead torrent still come back, just
// marked not approved with their rejection reason; the legitimate FLAC
// release and the format-less one (real music release titles routinely
// name the source — "SHM-CD", "24-96 hdtracks" — rather than the codec, so
// an unstated format must not be an automatic rejection; see
// internal/release.PreferencesFor) are approved, best-scored first.
func TestSearchWantedMusicAlbumScoresAndFilters(t *testing.T) {
	a := newTestAPI(t)
	mock := mockTorznabIndexer(t, musicSearchXML)

	a.want(a.call("POST", "/api/v1/indexer", map[string]any{
		"name": "Mock", "type": "torznab", "baseUrl": mock.URL, "enabled": true,
	}, nil), http.StatusCreated)

	wantedID := seedWantedAlbum(t, a)

	var resp struct {
		Releases []struct {
			Title      string   `json:"title"`
			GUID       string   `json:"guid"`
			Approved   bool     `json:"approved"`
			Score      int      `json:"score"`
			Rejections []string `json:"rejections,omitempty"`
		} `json:"releases"`
		Errors []string `json:"errors"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/wanted/%d/search", wantedID), nil, &resp), http.StatusOK)

	if len(resp.Releases) != 4 {
		t.Fatalf("releases = %+v, want all 4 candidates back (approved and rejected)", resp.Releases)
	}
	if !resp.Releases[0].Approved || resp.Releases[0].GUID != "https://mock/torrent/good" {
		t.Errorf("best-scored survivor = %+v, want the good (FLAC) release first, approved", resp.Releases[0])
	}
	if !resp.Releases[1].Approved || resp.Releases[1].GUID != "https://mock/torrent/unknown" {
		t.Errorf("second survivor = %+v, want the format-less release, approved", resp.Releases[1])
	}
	for _, r := range resp.Releases[2:] {
		if r.Approved || len(r.Rejections) == 0 {
			t.Errorf("release %+v: want rejected with a reason", r)
		}
	}
}

// TestSearchWantedMusicAlbumFiltersBlocklisted: a release matching a
// blocklist entry (by guid) must never be re-offered, even if it would
// otherwise score fine — the other real bug found alongside the scoring
// gap: nothing anywhere called download.Store.BlockedKeys/IsBlocked either.
func TestSearchWantedMusicAlbumFiltersBlocklisted(t *testing.T) {
	a := newTestAPI(t)
	mock := mockTorznabIndexer(t, musicSearchXML)

	a.want(a.call("POST", "/api/v1/indexer", map[string]any{
		"name": "Mock", "type": "torznab", "baseUrl": mock.URL, "enabled": true,
	}, nil), http.StatusCreated)

	if _, err := a.db.Exec(`INSERT INTO blocklist (guid, title, reason) VALUES (?, ?, ?)`,
		"https://mock/torrent/good", "Boards of Canada - Geogaddi FLAC", "test"); err != nil {
		t.Fatalf("seed blocklist: %v", err)
	}

	wantedID := seedWantedAlbum(t, a)

	var resp struct {
		Releases []struct {
			GUID string `json:"guid"`
		} `json:"releases"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/wanted/%d/search", wantedID), nil, &resp), http.StatusOK)

	for _, r := range resp.Releases {
		if r.GUID == "https://mock/torrent/good" {
			t.Errorf("blocklisted release was re-offered: %+v", resp.Releases)
		}
	}
}
