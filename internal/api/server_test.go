package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/scanner"
)

// testServer wires up a Server against a real in-memory database and a
// scanner pointed at a local MusicBrainz stand-in (mbHandler, or a 404
// default if nil), for tests that don't care about matching specifics.
func testServer(t *testing.T, mbHandler http.HandlerFunc) (*Server, *database.DB, string) {
	t.Helper()

	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if mbHandler == nil {
		mbHandler = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }
	}
	mbSrv := httptest.NewServer(mbHandler)
	t.Cleanup(mbSrv.Close)
	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", mbSrv.URL)

	sc := scanner.New(db, mb, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = "test-api-key"
	configPath := filepath.Join(t.TempDir(), "config.yaml")

	s := NewServer("test", db, sc, cfg, configPath)
	return s, db, cfg.APIKey
}

func doRequest(t *testing.T, s *Server, method, path, apiKey string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestHealthIsUnauthenticated(t *testing.T) {
	s, _, _ := testServer(t, nil)
	rec := doRequest(t, s, "GET", "/api/v1/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuthRequired(t *testing.T) {
	s, _, _ := testServer(t, nil)
	rec := doRequest(t, s, "GET", "/api/v1/root-folders", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	rec = doRequest(t, s, "GET", "/api/v1/root-folders", "wrong-key", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status with wrong key = %d, want 401", rec.Code)
	}
}

func TestRootFolderCreateListDelete(t *testing.T) {
	s, _, apiKey := testServer(t, nil)
	dir := t.TempDir()

	rec := doRequest(t, s, "POST", "/api/v1/root-folders", apiKey, createRootFolderRequest{Path: dir})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var rf database.RootFolder
	if err := json.Unmarshal(rec.Body.Bytes(), &rf); err != nil {
		t.Fatal(err)
	}
	if rf.Path != dir {
		t.Errorf("Path = %q, want %q", rf.Path, dir)
	}

	rec = doRequest(t, s, "GET", "/api/v1/root-folders", apiKey, nil)
	var list []database.RootFolder
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}

	rec = doRequest(t, s, "DELETE", "/api/v1/root-folders/"+itoa(rf.ID), apiKey, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", rec.Code)
	}
}

func TestCreateRootFolderRejectsNonexistentPath(t *testing.T) {
	s, _, apiKey := testServer(t, nil)
	rec := doRequest(t, s, "POST", "/api/v1/root-folders", apiKey, createRootFolderRequest{Path: "/definitely/does/not/exist/anywhere"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestLibraryBrowseEndpoints(t *testing.T) {
	s, db, apiKey := testServer(t, nil)
	ctx := t.Context()

	artist, err := db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := db.GetOrCreateTrack(ctx, album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := db.CreateRootFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tf, err := db.UpsertTrackFileByPath(ctx, rf.ID, filepath.Join(rf.Path, "song.flac"), 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTrackFileMatch(ctx, tf.ID, &track.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s, "GET", "/api/v1/artists", apiKey, nil)
	var artists []database.Artist
	json.Unmarshal(rec.Body.Bytes(), &artists)
	if len(artists) != 1 || artists[0].ID != artist.ID {
		t.Errorf("artists = %+v", artists)
	}

	rec = doRequest(t, s, "GET", "/api/v1/artists/"+itoa(artist.ID)+"/albums", apiKey, nil)
	var albums []database.Album
	json.Unmarshal(rec.Body.Bytes(), &albums)
	if len(albums) != 1 || albums[0].ID != album.ID {
		t.Errorf("albums = %+v", albums)
	}

	rec = doRequest(t, s, "GET", "/api/v1/albums/"+itoa(album.ID)+"/tracks", apiKey, nil)
	var tracks []database.Track
	json.Unmarshal(rec.Body.Bytes(), &tracks)
	if len(tracks) != 1 || tracks[0].ID != track.ID {
		t.Errorf("tracks = %+v", tracks)
	}

	rec = doRequest(t, s, "GET", "/api/v1/tracks/"+itoa(track.ID)+"/files", apiKey, nil)
	var files []database.TrackFile
	json.Unmarshal(rec.Body.Bytes(), &files)
	if len(files) != 1 || files[0].ID != tf.ID {
		t.Errorf("files = %+v", files)
	}
}

func TestUnmatchedAndManualMatchFlow(t *testing.T) {
	rec := sampleMBRecording()
	s, db, apiKey := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rec)
	})
	ctx := t.Context()

	rf, err := db.CreateRootFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tf, err := db.UpsertTrackFileByPath(ctx, rf.ID, filepath.Join(rf.Path, "song.flac"), 1, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}

	resp := doRequest(t, s, "GET", "/api/v1/track-files/unmatched", apiKey, nil)
	var unmatched []database.TrackFile
	json.Unmarshal(resp.Body.Bytes(), &unmatched)
	if len(unmatched) != 1 {
		t.Fatalf("unmatched = %+v, want 1", unmatched)
	}

	resp = doRequest(t, s, "POST", "/api/v1/track-files/"+itoa(tf.ID)+"/match", apiKey, manualMatchRequest{RecordingMBID: "rec-mbid"})
	if resp.Code != http.StatusOK {
		t.Fatalf("manual match status = %d, body = %s", resp.Code, resp.Body.String())
	}

	got, err := db.GetTrackFile(ctx, tf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchStatus != database.StatusManual {
		t.Errorf("MatchStatus = %q, want manual", got.MatchStatus)
	}

	resp = doRequest(t, s, "DELETE", "/api/v1/track-files/"+itoa(tf.ID)+"/match", apiKey, nil)
	if resp.Code != http.StatusNoContent {
		t.Errorf("clear match status = %d", resp.Code)
	}
	got, _ = db.GetTrackFile(ctx, tf.ID)
	if got.MatchStatus != database.StatusUnmatched {
		t.Errorf("MatchStatus after clear = %q, want unmatched", got.MatchStatus)
	}
}

func TestScanTriggerAndStatus(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	rec := doRequest(t, s, "POST", "/api/v1/scan", apiKey, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger status = %d, want 202", rec.Code)
	}

	// A second concurrent trigger should be refused while the first is
	// still (at least potentially) running.
	rec2 := doRequest(t, s, "POST", "/api/v1/scan", apiKey, nil)
	if rec2.Code != http.StatusConflict && rec2.Code != http.StatusAccepted {
		t.Errorf("second trigger status = %d, want 202 or 409", rec2.Code)
	}

	statusRec := doRequest(t, s, "GET", "/api/v1/scan/status", apiKey, nil)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d", statusRec.Code)
	}
	var state scanState
	if err := json.Unmarshal(statusRec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsGetAndUpdate(t *testing.T) {
	s, _, apiKey := testServer(t, nil)

	rec := doRequest(t, s, "GET", "/api/v1/settings", apiKey, nil)
	var view settingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.NamingFormat == "" {
		t.Error("expected a default naming_format")
	}

	view.ScanIntervalHours = 12
	view.NamingFormat = "{Artist}/{Title}.{Ext}"
	view.MinMatchConfidence = 0.5
	rec = doRequest(t, s, "PUT", "/api/v1/settings", apiKey, view)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, s, "GET", "/api/v1/settings", apiKey, nil)
	var updated settingsView
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.ScanIntervalHours != 12 || updated.NamingFormat != "{Artist}/{Title}.{Ext}" {
		t.Errorf("updated settings = %+v", updated)
	}
}

func TestSettingsUpdateRejectsInvalid(t *testing.T) {
	s, _, apiKey := testServer(t, nil)
	rec := doRequest(t, s, "GET", "/api/v1/settings", apiKey, nil)
	var view settingsView
	json.Unmarshal(rec.Body.Bytes(), &view)

	view.LogLevel = "not-a-real-level"
	rec = doRequest(t, s, "PUT", "/api/v1/settings", apiKey, view)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid log_level", rec.Code)
	}
}

func sampleMBRecording() musicbrainz.Recording {
	return musicbrainz.Recording{
		ID:     "rec-mbid",
		Title:  "Alpha and Omega",
		Length: 200000,
		ArtistCredit: []musicbrainz.ArtistCredit{
			{Name: "Boards of Canada", Artist: musicbrainz.ArtistRef{ID: "artist-mbid", Name: "Boards of Canada", SortName: "Boards of Canada"}},
		},
		Releases: []musicbrainz.Release{
			{ID: "release-mbid", Title: "Geogaddi", Date: "2002-02-04", ReleaseGroup: musicbrainz.ReleaseGroup{ID: "rg-mbid", Title: "Geogaddi", PrimaryType: "Album"}},
		},
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
