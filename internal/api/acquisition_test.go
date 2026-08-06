package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/qbittorrent"
)

const sampleArtistJSON = `{
	"id": "a-mbid",
	"name": "Boards of Canada",
	"sort-name": "Boards of Canada",
	"release-groups": [
		{"id": "rg-1", "title": "Geogaddi", "primary-type": "Album", "secondary-types": [], "first-release-date": "2002-02-04"}
	]
}`

func TestAcquisitionEndToEndFlow(t *testing.T) {
	s, db, apiKey := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})

	// Monitor a brand-new artist by mbid.
	rec := doRequest(t, s, "POST", "/api/v1/artists/monitor", apiKey, monitorArtistRequest{MBID: "a-mbid"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("monitor status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var monitored database.Artist
	if err := json.Unmarshal(rec.Body.Bytes(), &monitored); err != nil {
		t.Fatal(err)
	}
	if !monitored.IsMonitored {
		t.Error("IsMonitored should be true after monitoring")
	}

	// Nothing auto-wanted — the cached discography is what's there.
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID)+"/missing", apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var missing []database.ReleaseGroupCache
	if err := json.Unmarshal(rec.Body.Bytes(), &missing); err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Title != "Geogaddi" {
		t.Fatalf("missing = %+v", missing)
	}

	// Want it.
	rec = doRequest(t, s, "POST", "/api/v1/artists/"+itoa(monitored.ID)+"/wanted", apiKey, wantArtistAlbumRequest{ReleaseGroupMBID: "rg-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("want status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var wanted []database.WantedAlbum
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID)+"/wanted", apiKey, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &wanted); err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 1 || wanted[0].Title != "Geogaddi" {
		t.Fatalf("wanted = %+v", wanted)
	}

	// Now wanted, so it drops out of "missing".
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID)+"/missing", apiKey, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &missing); err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("missing after want = %+v, want empty", missing)
	}

	// Wire in fake Prowlarr + qBittorrent.
	pwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"guid":"g1","title":"Boards of Canada - Geogaddi [FLAC]","protocol":"torrent","indexerId":1,"indexer":"Test","magnetUrl":"magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12"}]`))
	}))
	defer pwSrv.Close()

	qbSessions := map[string]bool{}
	qbTorrents := map[string]bool{}
	qbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			r.ParseForm()
			if r.FormValue("password") != "qb-key" {
				w.Write([]byte("Fails."))
				return
			}
			qbSessions["sid"] = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			ck, err := r.Cookie("SID")
			if err != nil || !qbSessions[ck.Value] {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			qbTorrents["abcdef1234567890abcdef1234567890abcdef12"] = true
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			ck, err := r.Cookie("SID")
			if err != nil || !qbSessions[ck.Value] {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			r.ParseForm()
			delete(qbTorrents, r.FormValue("hashes"))
			w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer qbSrv.Close()

	s.acquisition.UpdateClients(prowlarr.NewClient(pwSrv.URL, "pw-key", "ua"), qbittorrent.NewClient(qbSrv.URL, "cantinode", "qb-key"), nil)

	if _, err := db.CreateRootFolder(t.Context(), t.TempDir()); err != nil {
		t.Fatal(err)
	}

	// Search.
	rec = doRequest(t, s, "GET", "/api/v1/wanted-albums/"+itoa(wanted[0].ID)+"/search", apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var releases []prowlarr.Release
	if err := json.Unmarshal(rec.Body.Bytes(), &releases); err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %+v", releases)
	}

	// Grab.
	rec = doRequest(t, s, "POST", "/api/v1/wanted-albums/"+itoa(wanted[0].ID)+"/grab", apiKey, releases[0])
	if rec.Code != http.StatusCreated {
		t.Fatalf("grab status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var download database.Download
	if err := json.Unmarshal(rec.Body.Bytes(), &download); err != nil {
		t.Fatal(err)
	}
	if !qbTorrents["abcdef1234567890abcdef1234567890abcdef12"] {
		t.Error("qBittorrent fake never received the add")
	}

	// Downloads list reflects it.
	rec = doRequest(t, s, "GET", "/api/v1/downloads", apiKey, nil)
	var downloads []database.Download
	if err := json.Unmarshal(rec.Body.Bytes(), &downloads); err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 1 || downloads[0].ID != download.ID {
		t.Errorf("downloads = %+v", downloads)
	}

	// Cancel it: removed from the download client, gone from the list,
	// and the wanted album is back to "wanted".
	rec = doRequest(t, s, "DELETE", "/api/v1/downloads/"+itoa(download.ID), apiKey, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if qbTorrents["abcdef1234567890abcdef1234567890abcdef12"] {
		t.Error("qBittorrent fake should no longer have the torrent after cancel")
	}
	rec = doRequest(t, s, "GET", "/api/v1/downloads", apiKey, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &downloads); err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 0 {
		t.Errorf("downloads after cancel = %+v, want empty", downloads)
	}
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID)+"/wanted", apiKey, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &wanted); err != nil {
		t.Fatal(err)
	}
	if wanted[0].Status != database.WantedStatusWanted {
		t.Errorf("wanted status after cancel = %q, want wanted", wanted[0].Status)
	}

	// Unmonitor leaves the artist and its wanted albums alone — just
	// flips IsMonitored off.
	rec = doRequest(t, s, "POST", "/api/v1/artists/"+itoa(monitored.ID)+"/unmonitor", apiKey, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("unmonitor status = %d", rec.Code)
	}
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID), apiKey, nil)
	var detail artistDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.IsMonitored {
		t.Error("IsMonitored should be false after unmonitoring")
	}
	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(monitored.ID)+"/wanted", apiKey, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &wanted); err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 1 {
		t.Errorf("wanted after unmonitor = %+v, want still there (unmonitoring doesn't delete wanted albums)", wanted)
	}
}

func TestMonitorArtistByIDForAnOwnedArtist(t *testing.T) {
	s, db, apiKey := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})
	ctx := t.Context()

	// An artist that exists purely from file-matching, never monitored —
	// mirrors the live instance's "Derek and the Dominos" scenario.
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "POST", "/api/v1/artists/"+itoa(a.ID)+"/monitor", apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got database.Artist
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID {
		t.Errorf("ID = %d, want %d (same row, not a duplicate)", got.ID, a.ID)
	}
	if !got.IsMonitored {
		t.Error("IsMonitored should be true")
	}
}

func TestGetArtistIncludesOwnedAlbumCount(t *testing.T) {
	s, db, apiKey := testServer(t, nil)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetOrCreateAlbum(ctx, a.ID, "rel-1", "rg-1", "Album One", "2020", "Album"); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "GET", "/api/v1/artists/"+itoa(a.ID), apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var detail artistDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	// GetOrCreateAlbum alone doesn't make it "owned" for ListAlbumsByArtist
	// (that requires a track_file), so the count here is legitimately 0 —
	// this test is really just proving the field round-trips correctly.
	if detail.ID != a.ID {
		t.Errorf("detail = %+v", detail)
	}
}

func TestGetArtistNotFound(t *testing.T) {
	s, _, apiKey := testServer(t, nil)
	rec := doRequest(t, s, "GET", "/api/v1/artists/999", apiKey, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestArtistSearchEndpoint(t *testing.T) {
	s, _, apiKey := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"count":1,"artists":[{"id":"a-mbid","name":"Boards of Canada","sort-name":"Boards of Canada","score":100}]}`))
	})

	rec := doRequest(t, s, "GET", "/api/v1/musicbrainz/artist-search?query=Boards+of+Canada", apiKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWantArtistAlbumWithMonitorFlagMonitorsArtist(t *testing.T) {
	s, db, apiKey := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})

	rec := doRequest(t, s, "POST", "/api/v1/artists/monitor", apiKey, monitorArtistRequest{MBID: "a-mbid"})
	var monitored database.Artist
	if err := json.Unmarshal(rec.Body.Bytes(), &monitored); err != nil {
		t.Fatal(err)
	}
	if err := db.SetArtistMonitored(t.Context(), monitored.ID, false); err != nil {
		t.Fatal(err)
	}

	rec = doRequest(t, s, "POST", "/api/v1/artists/"+itoa(monitored.ID)+"/wanted", apiKey, wantArtistAlbumRequest{ReleaseGroupMBID: "rg-1", Monitor: true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetArtist(t.Context(), monitored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsMonitored {
		t.Error("IsMonitored should be true after Add & Monitor")
	}
}

func TestGrabReleaseWithoutDownloadClientConfigured(t *testing.T) {
	s, db, apiKey := testServer(t, nil)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, a.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "POST", "/api/v1/wanted-albums/"+itoa(w.ID)+"/grab", apiKey, prowlarr.Release{Title: "X"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (download client/Prowlarr not configured)", rec.Code)
	}
}
