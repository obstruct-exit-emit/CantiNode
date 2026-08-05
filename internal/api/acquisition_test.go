package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cantinode/cantinode/internal/acervinode"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/prowlarr"
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

	// Monitor the artist.
	rec := doRequest(t, s, "POST", "/api/v1/monitored-artists", apiKey, monitorArtistRequest{MBID: "a-mbid"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("monitor status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var monitored database.MonitoredArtist
	if err := json.Unmarshal(rec.Body.Bytes(), &monitored); err != nil {
		t.Fatal(err)
	}

	// Wanted albums were seeded.
	rec = doRequest(t, s, "GET", "/api/v1/monitored-artists/"+itoa(monitored.ID)+"/wanted", apiKey, nil)
	var wanted []database.WantedAlbum
	if err := json.Unmarshal(rec.Body.Bytes(), &wanted); err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 1 || wanted[0].Title != "Geogaddi" {
		t.Fatalf("wanted = %+v", wanted)
	}

	// Wire in fake Prowlarr + AcerviNode.
	pwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"guid":"g1","title":"Boards of Canada - Geogaddi [FLAC]","protocol":"torrent","indexerId":1,"indexer":"Test","magnetUrl":"magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12"}]`))
	}))
	defer pwSrv.Close()

	avSessions := map[string]bool{}
	avTorrents := map[string]bool{}
	avSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			r.ParseForm()
			if r.FormValue("password") != "av-key" {
				w.Write([]byte("Fails."))
				return
			}
			avSessions["sid"] = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			ck, err := r.Cookie("SID")
			if err != nil || !avSessions[ck.Value] {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			avTorrents["abcdef1234567890abcdef1234567890abcdef12"] = true
			w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer avSrv.Close()

	s.acquisition.UpdateClients(prowlarr.NewClient(pwSrv.URL, "pw-key", "ua"), acervinode.NewClient(avSrv.URL, "av-key"))

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
	if !avTorrents["abcdef1234567890abcdef1234567890abcdef12"] {
		t.Error("AcerviNode fake never received the add")
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

	// Unmonitor cleans up.
	rec = doRequest(t, s, "DELETE", "/api/v1/monitored-artists/"+itoa(monitored.ID), apiKey, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("unmonitor status = %d", rec.Code)
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

func TestGrabReleaseWithoutAcerviNodeConfigured(t *testing.T) {
	s, db, apiKey := testServer(t, nil)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "POST", "/api/v1/wanted-albums/"+itoa(w.ID)+"/grab", apiKey, prowlarr.Release{Title: "X"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (AcerviNode/Prowlarr not configured)", rec.Code)
	}
}
