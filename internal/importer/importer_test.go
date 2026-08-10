package importer

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
)

// mockSab fakes just enough of SABnzbd's API for Service.PollOnce to see one
// finished download: an empty queue and one "Completed" history slot whose
// storage path is the real source directory under test. deleteCalls counts
// how many "delete" actions the server received (queue+history delete both
// count, matching sabnzbd.Remove's own two calls), for tests to assert the
// importer actually cleaned up the client side after a successful import.
func mockSab(t *testing.T, storagePath, status string) (srv *httptest.Server, deleteCalls *int) {
	t.Helper()
	deleteCalls = new(int)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("name") == "delete" {
			*deleteCalls++
			w.Write([]byte(`{"status": true}`))
			return
		}
		switch q.Get("mode") {
		case "queue":
			w.Write([]byte(`{"queue":{"slots":[]}}`))
		case "history":
			w.Write([]byte(`{"history":{"slots":[{
				"nzo_id": "nzo1",
				"name": "Test Album",
				"category": "cantinode",
				"status": "` + status + `",
				"storage": "` + storagePath + `"
			}]}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, deleteCalls
}

// setup builds a full Service against temp databases/directories: a source
// "download" directory with one real file (standing in for a finished
// download's content) and a destination music root folder.
func setup(t *testing.T, sab *httptest.Server) (*Service, *download.Store, *musiclibrary.Store, string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	dlStore := download.NewStore(db)
	if err := dlStore.Add(&download.ClientConfig{
		Name: "Sabnzb", Type: download.TypeSABnzbd, Host: sab.URL,
		APIKey: "key", Category: "cantinode", Enabled: true, Priority: 1,
	}); err != nil {
		t.Fatalf("add download client: %v", err)
	}
	downloads := download.NewService(dlStore)

	musicStore := musiclibrary.NewStore(db)
	destRoot := t.TempDir()
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, destRoot); err != nil {
		t.Fatalf("seed root folder: %v", err)
	}

	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", "http://127.0.0.1:0")
	scanner := musicscanner.New(musicStore, mb, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false)

	cfg := &config.Config{}

	return New(downloads, scanner, musicStore, cfg), dlStore, musicStore, destRoot
}

func TestPollOnceImportsCompletedDownload(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Test Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain non-audio file is enough: it proves the copy happened without
	// requiring a real (fake-MusicBrainz-backed) match — musicscanner skips
	// non-audio files outright (tagreader.IsAudioFile), so ScanAll runs
	// clean with zero network calls.
	if err := os.WriteFile(filepath.Join(albumDir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	sab, deleteCalls := mockSab(t, albumDir, "Completed")
	svc, dlStore, musicStore, destRoot := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatalf("set wanted album downloading: %v", err)
	}

	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Checked != 1 || result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("PollOnce result = %+v, want 1 checked, 1 imported, 0 failed", result)
	}

	destFile := filepath.Join(destRoot, "Test Album", "readme.txt")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("copied file not found at %s: %v", destFile, err)
	}

	grabs, err := dlStore.ListGrabs(download.GrabStatusImported)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("imported grabs = %+v, want exactly 1", grabs)
	}

	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != musiclibrary.WantedStatusDownloaded {
		t.Errorf("wanted album status = %q, want %q", got.Status, musiclibrary.WantedStatusDownloaded)
	}

	if *deleteCalls == 0 {
		t.Error("importer should have removed the completed download from its client after importing it")
	}

	// mockSab's own "delete" handler never touches the filesystem (exactly
	// like a debrid bridge that acknowledges deleteData but ignores it) —
	// the source directory must still be gone, proving the importer's own
	// direct deleteDownloadData fallback ran rather than trusting the
	// client did it.
	if _, err := os.Stat(albumDir); !os.IsNotExist(err) {
		t.Errorf("source directory %s should have been deleted directly, stat err = %v", albumDir, err)
	}
}

func TestPollOnceAppliesPathMapping(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Mapped Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "track.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The download client reports a path under a prefix the mapping
	// translates to the real source directory — same shape as a debrid
	// bridge reporting "/storage_1/downloads/..." for what this machine
	// actually sees mounted elsewhere. The mapping's local prefix is src
	// (albumDir's parent): TranslatePath keeps the remainder after the
	// matched prefix, so "<remote prefix>/Mapped Album" resolves to
	// "<src>/Mapped Album", i.e. albumDir itself.
	remotePath := "/storage_1/downloads/torbox/cantinode/Mapped Album"
	sab, _ := mockSab(t, remotePath, "Completed")
	svc, dlStore, _, destRoot := setup(t, sab)
	svc.cfg = testConfigWithMapping(t, "/storage_1/downloads/torbox/cantinode", src)

	if err := dlStore.AddGrab(&download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "nzo1", Title: "Mapped Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Imported != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 imported", result)
	}

	destFile := filepath.Join(destRoot, filepath.Base(albumDir), "track.txt")
	if _, err := os.Stat(destFile); err != nil {
		t.Errorf("mapped copy not found at %s: %v", destFile, err)
	}
}

// testConfigWithMapping builds a *config.Config with one path mapping,
// persisted to a throwaway data dir so SetPathMappings has somewhere to save.
func testConfigWithMapping(t *testing.T, remote, local string) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.SetPathMappings([]config.PathMapping{{RemotePrefix: remote, LocalPrefix: local}}); err != nil {
		t.Fatalf("SetPathMappings: %v", err)
	}
	return cfg
}

func TestPollOnceMarksClientReportedFailureAsFailed(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Failed")
	svc, dlStore, musicStore, _ := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatalf("set wanted album downloading: %v", err)
	}

	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Failed != 1 || result.Imported != 0 {
		t.Fatalf("PollOnce result = %+v, want 1 failed, 0 imported", result)
	}
	grabs, err := dlStore.ListGrabs(download.GrabStatusFailed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("failed grabs = %+v, want exactly 1", grabs)
	}

	// A failed grab reverts its wanted album back to "wanted" so the user
	// can search again and try a different release, instead of it staying
	// stuck at "downloading" forever.
	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want %q", got.Status, musiclibrary.WantedStatusWanted)
	}
}

func TestPollOnceIgnoresGrabsStillInProgress(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Downloading")
	svc, dlStore, _, _ := setup(t, sab)

	if err := dlStore.AddGrab(&download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	// "Downloading" (still in the queue, not the history) never shows up
	// with a completed/failed status in this fake, so PollOnce must leave
	// it alone rather than treating "not finished yet" as an orphan.
	result := svc.PollOnce(t.Context())
	if result.Imported != 0 || result.Failed != 0 {
		t.Fatalf("PollOnce result = %+v, want no action on an in-progress grab", result)
	}
	grabs, err := dlStore.ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("still-grabbed grabs = %+v, want exactly 1 untouched", grabs)
	}
}

func TestPollOnceNoGrabsIsANoop(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Completed")
	svc, _, _, _ := setup(t, sab)

	result := svc.PollOnce(t.Context())
	if result != (PollResult{}) {
		t.Errorf("result = %+v, want zero value when there's nothing to check", result)
	}
}

func TestDeleteDownloadDataRefusesEmptyAndRelativePaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "marker")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "relative/path"} {
		deleteDownloadData(p, slog.Default())
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("unrelated marker file should be untouched: %v", err)
	}
}

func TestDeleteDownloadDataRefusesShallowAbsolutePath(t *testing.T) {
	// os.MkdirTemp("", ...) lands directly under the OS temp root (typically
	// /tmp on Linux) — exactly two path segments deep, below the "at least
	// three" floor deleteDownloadData enforces. t.TempDir() isn't used here
	// specifically because its own depth varies by environment; this test
	// needs a path of a known, controlled shallowness to prove the guard
	// actually refuses it rather than happening not to touch it.
	shallow, err := os.MkdirTemp("", "importer-shallow-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shallow) })
	marker := filepath.Join(shallow, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleteDownloadData(shallow, slog.Default())

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a shallow path must be refused, not deleted: marker gone (%v)", err)
	}
}

func TestDeleteDownloadDataRemovesADeepAbsolutePath(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "downloads", "client", "release")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "file.flac"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleteDownloadData(deep, slog.Default())

	if _, err := os.Stat(deep); !os.IsNotExist(err) {
		t.Errorf("deep path should have been removed, stat err = %v", err)
	}
}

func TestCopyTreeCopiesNestedDirectories(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dest")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, rel := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing %s after copyTree: %v", rel, err)
		}
	}
}

func TestQueueKeyIsStable(t *testing.T) {
	if queueKey(1, "abc") == queueKey(2, "abc") {
		t.Error("different config ids must not collide")
	}
	if !strings.Contains(queueKey(1, "abc"), "abc") {
		t.Error("queueKey should carry the item id")
	}
}
