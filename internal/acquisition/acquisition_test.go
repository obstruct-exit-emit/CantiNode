package acquisition

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/acervinode"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/scanner"
)

const sampleArtistJSON = `{
	"id": "a-mbid",
	"name": "Boards of Canada",
	"sort-name": "Boards of Canada",
	"release-groups": [
		{"id": "rg-album-1", "title": "Music Has the Right to Children", "primary-type": "Album", "secondary-types": [], "first-release-date": "1998-04-20"},
		{"id": "rg-album-2", "title": "Geogaddi", "primary-type": "Album", "secondary-types": [], "first-release-date": "2002-02-04"},
		{"id": "rg-ep", "title": "In a Beautiful Place", "primary-type": "EP", "secondary-types": [], "first-release-date": "2000-01-01"},
		{"id": "rg-live", "title": "Live Bootleg", "primary-type": "Album", "secondary-types": ["Live"], "first-release-date": "1999-01-01"}
	]
}`

// newTestService wires a Service against an in-memory database and a
// stub MusicBrainz server (mbHandler, or 404-everything if nil). Its
// Scanner is real but never needs to actually match anything for these
// tests — a post-import scan against an untagged fixture file just
// records a per-file read error, which doesn't fail the scan itself
// (see internal/scanner's own tests for that behavior in depth).
func newTestService(t *testing.T, mbHandler http.HandlerFunc) (*Service, *database.DB) {
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

	return New(db, mb, sc, nil), db
}

func TestMonitorArtistSeedsOnlyPlainAlbums(t *testing.T) {
	s, db := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})
	ctx := t.Context()

	m, err := s.MonitorArtist(ctx, "a-mbid")
	if err != nil {
		t.Fatalf("MonitorArtist: %v", err)
	}
	if m.Name != "Boards of Canada" {
		t.Errorf("Name = %q", m.Name)
	}

	wanted, err := db.ListWantedAlbumsByArtist(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 2 {
		t.Fatalf("len(wanted) = %d, want 2 (only the two plain Albums, not the EP or Live album)", len(wanted))
	}
	titles := map[string]bool{}
	for _, w := range wanted {
		titles[w.Title] = true
		if w.Status != database.WantedStatusWanted {
			t.Errorf("%s status = %q, want wanted", w.Title, w.Status)
		}
	}
	if !titles["Music Has the Right to Children"] || !titles["Geogaddi"] {
		t.Errorf("titles = %v, missing expected albums", titles)
	}
}

func TestSyncArtistDoesNotResetNonWantedStatus(t *testing.T) {
	s, db := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})
	ctx := t.Context()

	m, err := s.MonitorArtist(ctx, "a-mbid")
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := db.ListWantedAlbumsByArtist(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetWantedAlbumStatus(ctx, wanted[0].ID, database.WantedStatusDownloaded); err != nil {
		t.Fatal(err)
	}

	if err := s.SyncArtist(ctx, m.ID); err != nil {
		t.Fatalf("SyncArtist: %v", err)
	}

	got, err := db.GetWantedAlbum(ctx, wanted[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != database.WantedStatusDownloaded {
		t.Errorf("status after re-sync = %q, want downloaded (must not be reset)", got.Status)
	}

	refreshed, err := db.GetMonitoredArtist(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.LastSyncedAt == nil {
		t.Error("LastSyncedAt should be set after SyncArtist")
	}
}

func TestSearchReleasesRequiresProwlarr(t *testing.T) {
	s, db := newTestService(t, nil)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.SearchReleases(ctx, w.ID); err != errProwlarrNotConfigured {
		t.Errorf("err = %v, want errProwlarrNotConfigured", err)
	}
}

func TestSearchReleasesUsesArtistAndAlbumName(t *testing.T) {
	s, db := newTestService(t, nil)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Geogaddi", "Album", "2002")
	if err != nil {
		t.Fatal(err)
	}

	var gotQuery string
	pwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer pwSrv.Close()
	s.UpdateClients(prowlarr.NewClient(pwSrv.URL, "key", "ua"), nil)

	if _, err := s.SearchReleases(ctx, w.ID); err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if gotQuery != "Boards of Canada Geogaddi" {
		t.Errorf("query = %q, want %q", gotQuery, "Boards of Canada Geogaddi")
	}
}

// fakeAcervi is a minimal stand-in for AcerviNode's qBittorrent shim —
// just enough for GrabRelease/PollDownloads to exercise (login, add-by-
// magnet, status polling). The full protocol surface (session-expiry
// retry, torrent-file-by-diff, the SABnzbd shim) is already covered by
// internal/acervinode's own tests; this only needs to prove
// internal/acquisition orchestrates correctly against it.
type fakeAcervi struct {
	apiKey       string
	sessions     map[string]bool
	states       map[string]string // hash -> qBittorrent-shim state string
	contentPaths map[string]string // hash -> content_path override (defaults to a fake path)
}

func newFakeAcervi(apiKey string) *fakeAcervi {
	return &fakeAcervi{
		apiKey:       apiKey,
		sessions:     map[string]bool{},
		states:       map[string]string{},
		contentPaths: map[string]string{},
	}
}

func (f *fakeAcervi) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeAcervi) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/auth/login":
		r.ParseForm()
		if r.FormValue("password") != f.apiKey {
			w.Write([]byte("Fails."))
			return
		}
		f.sessions["sid"] = true
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
		w.Write([]byte("Ok."))

	case "/api/v2/torrents/add":
		if !f.authorized(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		r.ParseForm()
		hash, ok := strings.CutPrefix(r.FormValue("urls"), "magnet:?xt=urn:btih:")
		if !ok {
			w.Write([]byte("Fails."))
			return
		}
		f.states[hash] = "downloading"
		w.Write([]byte("Ok."))

	case "/api/v2/torrents/info":
		if !f.authorized(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		hash := r.URL.Query().Get("hashes")
		type item struct {
			Hash        string `json:"hash"`
			State       string `json:"state"`
			ContentPath string `json:"content_path"`
		}
		var out []item
		if state, ok := f.states[hash]; ok {
			path := f.contentPaths[hash]
			if path == "" {
				path = "/av-downloads/" + hash
			}
			out = append(out, item{Hash: hash, State: state, ContentPath: path})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAcervi) authorized(r *http.Request) bool {
	ck, err := r.Cookie("SID")
	return err == nil && f.sessions[ck.Value]
}

// grabTestFixtures wires a Service with a monitored artist + wanted
// album, a root folder, and both external clients pointed at fakes — a
// no-op Prowlarr stand-in (FetchContent never calls back into Prowlarr
// for a direct magnet URI) and fakeAcervi.
func grabTestFixtures(t *testing.T) (s *Service, db *database.DB, wantedAlbumID int64, av *fakeAcervi) {
	t.Helper()
	s, db = newTestService(t, nil)
	ctx := t.Context()

	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateRootFolder(ctx, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	pwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(pwSrv.Close)

	av = newFakeAcervi("av-key")
	avURL := av.start(t)

	s.UpdateClients(prowlarr.NewClient(pwSrv.URL, "key", "ua"), acervinode.NewClient(avURL, "av-key"))
	return s, db, w.ID, av
}

func TestGrabReleaseCreatesDownloadAndMarksDownloading(t *testing.T) {
	s, db, wantedAlbumID, _ := grabTestFixtures(t)
	ctx := t.Context()

	rel := prowlarr.Release{
		Title:     "Artist - Album [FLAC]",
		Indexer:   "TestIndexer",
		Protocol:  prowlarr.ProtocolTorrent,
		MagnetURL: "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12",
	}

	d, err := s.GrabRelease(ctx, wantedAlbumID, rel)
	if err != nil {
		t.Fatalf("GrabRelease: %v", err)
	}
	if d.Protocol != database.ProtocolTorrent || d.ClientID != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Errorf("d = %+v", d)
	}

	w, err := db.GetWantedAlbum(ctx, wantedAlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != database.WantedStatusDownloading {
		t.Errorf("wanted status = %q, want downloading", w.Status)
	}
}

func TestGrabReleaseRequiresRootFolder(t *testing.T) {
	s, db := newTestService(t, nil)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateClients(prowlarr.NewClient("http://unused.invalid", "k", "ua"), acervinode.NewClient("http://unused.invalid", "k"))

	rel := prowlarr.Release{Title: "X", MagnetURL: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if _, err := s.GrabRelease(ctx, w.ID, rel); err == nil {
		t.Error("expected an error with no root folders configured")
	}
}

func TestPollDownloadsImportsCompletedDownload(t *testing.T) {
	s, db, wantedAlbumID, av := grabTestFixtures(t)
	ctx := t.Context()

	rel := prowlarr.Release{
		Title:     "Artist - Album [FLAC]",
		Protocol:  prowlarr.ProtocolTorrent,
		MagnetURL: "magnet:?xt=urn:btih:1111111111111111111111111111111111111a",
	}
	d, err := s.GrabRelease(ctx, wantedAlbumID, rel)
	if err != nil {
		t.Fatal(err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "01 - Track.mp3"), []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	av.contentPaths[d.ClientID] = srcDir
	av.states[d.ClientID] = "pausedUP"

	result, err := s.PollDownloads(ctx)
	if err != nil {
		t.Fatalf("PollDownloads: %v", err)
	}
	if result.Checked != 1 || result.Imported != 1 || result.Errored != 0 {
		t.Errorf("result = %+v, want Checked=1 Imported=1 Errored=0", result)
	}

	rootFolder, err := db.GetRootFolder(ctx, d.RootFolderID)
	if err != nil {
		t.Fatal(err)
	}
	destFile := filepath.Join(rootFolder.Path, "_incoming", "download-"+strconv.FormatInt(d.ID, 10), "01 - Track.mp3")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("expected file copied to %s: %v", destFile, err)
	}

	gotDownload, err := db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDownload.Status != database.DownloadStatusImported || gotDownload.ImportedAt == nil {
		t.Errorf("download = %+v", gotDownload)
	}

	gotWanted, err := db.GetWantedAlbum(ctx, wantedAlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if gotWanted.Status != database.WantedStatusDownloaded {
		t.Errorf("wanted status = %q, want downloaded", gotWanted.Status)
	}
}

func TestPollDownloadsStillDownloadingIsNoOp(t *testing.T) {
	s, db, wantedAlbumID, _ := grabTestFixtures(t)
	ctx := t.Context()

	rel := prowlarr.Release{Title: "X", Protocol: prowlarr.ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:2222222222222222222222222222222222222a"}
	if _, err := s.GrabRelease(ctx, wantedAlbumID, rel); err != nil {
		t.Fatal(err)
	}

	result, err := s.PollDownloads(ctx)
	if err != nil {
		t.Fatalf("PollDownloads: %v", err)
	}
	if result.Checked != 1 || result.Imported != 0 || result.Errored != 0 {
		t.Errorf("result = %+v, want Checked=1 Imported=0 Errored=0", result)
	}

	w, err := db.GetWantedAlbum(ctx, wantedAlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != database.WantedStatusDownloading {
		t.Errorf("wanted status = %q, want still downloading", w.Status)
	}
}

func TestPollDownloadsNotFoundRevertsWantedToWanted(t *testing.T) {
	s, db, wantedAlbumID, av := grabTestFixtures(t)
	ctx := t.Context()

	rel := prowlarr.Release{Title: "X", Protocol: prowlarr.ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:3333333333333333333333333333333333333a"}
	d, err := s.GrabRelease(ctx, wantedAlbumID, rel)
	if err != nil {
		t.Fatal(err)
	}
	delete(av.states, d.ClientID) // simulate it vanishing from AcerviNode

	if _, err := s.PollDownloads(ctx); err != nil {
		t.Fatalf("PollDownloads: %v", err)
	}

	gotDownload, err := db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDownload.Status != database.DownloadStatusError {
		t.Errorf("download status = %q, want error", gotDownload.Status)
	}

	w, err := db.GetWantedAlbum(ctx, wantedAlbumID)
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != database.WantedStatusWanted {
		t.Errorf("wanted status = %q, want reverted to wanted", w.Status)
	}
}

func TestPollDownloadsNoOpWhenAcerviNotConfigured(t *testing.T) {
	s, db := newTestService(t, nil)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-1", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	rf, err := db.CreateRootFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateDownload(ctx, w.ID, rf.ID, database.ProtocolTorrent, "hash", "Title", "Indexer"); err != nil {
		t.Fatal(err)
	}

	result, err := s.PollDownloads(ctx)
	if err != nil {
		t.Fatalf("PollDownloads: %v", err)
	}
	if result.Checked != 0 {
		t.Errorf("Checked = %d, want 0 (AcerviNode not configured)", result.Checked)
	}
}
