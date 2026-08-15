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
	// An audio-extension file with garbage content is enough: copyTree only
	// filters by extension (tagreader.IsAudioFile), so it's copied over like
	// a real track would be; the scan step's tag-read then fails on the
	// garbage content, but that's a per-file scan error, not fatal — no real
	// (fake-MusicBrainz-backed) match is required for this test to prove the
	// copy itself happened.
	if err := os.WriteFile(filepath.Join(albumDir, "readme.flac"), []byte("hello"), 0o644); err != nil {
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

	destFile := filepath.Join(destRoot, "Test Album", "readme.flac")
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

	// The album is owned now (a real albums row exists) — its wanted_albums
	// row is deleted outright rather than left showing "downloaded" forever
	// in the Wanted card for something no longer actionable.
	if _, err := musicStore.GetWantedAlbum(wanted.ID); err != musiclibrary.ErrNotFound {
		t.Errorf("GetWantedAlbum after import: err = %v, want ErrNotFound (the row should be gone)", err)
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

// TestImportGrabSkipsWhenCanceledBeforeCopy is the regression test for a
// real race: removing an artist/album with a grab still in flight
// (internal/api's cancelInFlightGrabs) resolves that grab as failed, but
// importGrab used to finish the import anyway if it had already started
// — its own copy of the GrabRecord (fetched by PollOnce's own listing
// moments earlier) was stale, and it unconditionally overwrote the status
// back to "imported" at the end, plus ran ScanAll(), which recreates
// whatever was just removed straight from the copied files' own tags.
// importGrab must now re-check the grab's live status before doing
// anything slow and bail out if it's no longer "grabbed" — simulated here
// by resolving the grab in the store between building its GrabRecord (the
// stale snapshot PollOnce would have passed along) and calling importGrab
// directly with it.
func TestImportGrabSkipsWhenCanceledBeforeCopy(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Test Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "readme.flac"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	sab, _ := mockSab(t, albumDir, "Completed")
	svc, dlStore, _, destRoot := setup(t, sab)

	g := download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music", Status: download.GrabStatusGrabbed,
	}
	if err := dlStore.AddGrab(&g); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	// Simulate a concurrent artist/album removal resolving this exact grab
	// as failed after PollOnce already fetched its (now-stale) GrabRecord
	// above, but before importGrab actually runs.
	if err := dlStore.ResolveGrab(g.ID, download.GrabStatusFailed, "artist removed"); err != nil {
		t.Fatalf("resolve grab: %v", err)
	}

	item := download.Item{Client: "sabnzbd", ConfigID: 1, ID: "nzo1", Path: albumDir}
	if imported := svc.importGrab(t.Context(), g, item); imported {
		t.Error("importGrab should not report success for a grab that was already resolved elsewhere")
	}

	destFile := filepath.Join(destRoot, "Test Album", "readme.flac")
	if _, err := os.Stat(destFile); !os.IsNotExist(err) {
		t.Errorf("nothing should have been copied — stat err = %v", err)
	}

	got, err := dlStore.GetGrab(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != download.GrabStatusFailed {
		t.Errorf("grab status = %q, want failed (must not be overwritten back to imported)", got.Status)
	}
}

func TestPollOnceAppliesPathMapping(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Mapped Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "track.flac"), []byte("x"), 0o644); err != nil {
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

	destFile := filepath.Join(destRoot, filepath.Base(albumDir), "track.flac")
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
	if err := os.WriteFile(filepath.Join(src, "a.flac"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.mp3"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dest")
	copied, err := copyTree(src, dst)
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if copied != 2 {
		t.Errorf("copied = %d, want 2", copied)
	}

	for _, rel := range []string{"a.flac", filepath.Join("sub", "b.mp3")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing %s after copyTree: %v", rel, err)
		}
	}
}

// TestCopyTreeSkipsNonAudioFiles is the regression test for the actual
// feature: a download's NFOs, cover art, sidecar files, and sample/proof
// folders must never make it into the library — only the audio files do,
// and a subdirectory holding nothing else is never even created at the
// destination.
func TestCopyTreeSkipsNonAudioFiles(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"01 - Track.flac":         "audio",
		"release.nfo":             "junk",
		"cover.jpg":               "junk",
		"playlist.m3u":            "junk",
		"checksums.sfv":           "junk",
		"Sample/01 - Sample.mp3":  "junk-but-audio-extension", // still an audio file — copied
		"Sample/sample-proof.txt": "junk",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dst := filepath.Join(t.TempDir(), "dest")
	copied, err := copyTree(src, dst)
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if copied != 2 {
		t.Errorf("copied = %d, want 2 (the two audio files)", copied)
	}

	for _, rel := range []string{"01 - Track.flac", filepath.Join("Sample", "01 - Sample.mp3")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing audio file %s after copyTree: %v", rel, err)
		}
	}
	for _, rel := range []string{"release.nfo", "cover.jpg", "playlist.m3u", "checksums.sfv", filepath.Join("Sample", "sample-proof.txt")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("non-audio file %s should not have been copied, stat err = %v", rel, err)
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

// TestQueueKeyIsCaseInsensitive is the regression case for a real bug: a
// magnet's info hash is stored lowercase (download.magnetHash), but a
// debrid bridge routinely echoes qBittorrent's own torrent hash back in
// whatever case the original magnet used — a mismatch here makes PollOnce
// treat a perfectly healthy torrent as vanished from the queue.
func TestQueueKeyIsCaseInsensitive(t *testing.T) {
	if queueKey(1, "ABC123") != queueKey(1, "abc123") {
		t.Error("queueKey must normalize case so a bridge's differently-cased hash still matches")
	}
}

// TestPollOnceMatchesItemIDCaseInsensitively is the PollOnce-level version
// of the same regression: a grab recorded with an uppercase client item id
// (as a magnet's own hash was originally cased) must still match a queue
// item the client reports in a different case, and get imported rather
// than treated as an orphan.
func TestPollOnceMatchesItemIDCaseInsensitively(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Test Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "readme.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// mockSab's history slot always reports nzo_id "nzo1" (lowercase); the
	// grab below is recorded with the uppercase form on purpose.
	sab, _ := mockSab(t, albumDir, "Completed")
	svc, dlStore, _, _ := setup(t, sab)

	if err := dlStore.AddGrab(&download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "NZO1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("PollOnce result = %+v, want 1 imported despite the case mismatch", result)
	}
}

// TestSwapUpgradedFilesReplacesOnlyMatchedTracks exercises swapUpgradedFiles
// directly (full ScanAll-based matching would need a real MusicBrainz mock
// this package's test setup doesn't wire up): two tracks each start with
// one owned file; only one of them gets a genuinely new matched file (as if
// an upgrade grab's release only matched partially). The replaced track's
// old file must be deleted from disk and its track_files row removed; the
// untouched track's old file must survive untouched, so a partial upgrade
// can never leave a track with nothing.
func TestSwapUpgradedFilesReplacesOnlyMatchedTracks(t *testing.T) {
	sab, _ := mockSab(t, "", "Completed")
	svc, _, musicStore, root := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Test Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	trackReplaced, err := musicStore.GetOrCreateTrack(album.ID, "track-replaced-mbid", "Track One", 1, 1, 200000, "")
	if err != nil {
		t.Fatal(err)
	}
	trackUntouched, err := musicStore.GetOrCreateTrack(album.ID, "track-untouched-mbid", "Track Two", 2, 1, 200000, "")
	if err != nil {
		t.Fatal(err)
	}

	rootFolders, err := musicStore.ListRootFolders()
	if err != nil || len(rootFolders) == 0 {
		t.Fatalf("root folders = %+v, err %v", rootFolders, err)
	}
	rf := rootFolders[0]

	oldPathReplaced := filepath.Join(root, "old-track-one.flac")
	if err := os.WriteFile(oldPathReplaced, []byte("old audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTFReplaced, err := musicStore.UpsertTrackFileByPath(rf.ID, oldPathReplaced, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(oldTFReplaced.ID, &trackReplaced.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	oldPathUntouched := filepath.Join(root, "old-track-two.flac")
	if err := os.WriteFile(oldPathUntouched, []byte("old audio 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTFUntouched, err := musicStore.UpsertTrackFileByPath(rf.ID, oldPathUntouched, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(oldTFUntouched.ID, &trackUntouched.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	before, err := musicStore.ListTrackFilesByAlbum(album.ID)
	if err != nil || len(before) != 2 {
		t.Fatalf("before = %+v, err %v, want 2", before, err)
	}

	// The "upgrade" import: a new, better file lands for track one only —
	// track two's release didn't end up matching this round.
	newPath := filepath.Join(root, "new-track-one.flac")
	if err := os.WriteFile(newPath, []byte("shiny new audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	newTF, err := musicStore.UpsertTrackFileByPath(rf.ID, newPath, 200, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetTrackFileMatch(newTF.ID, &trackReplaced.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	svc.swapUpgradedFiles(album.ID, before)

	if _, statErr := os.Stat(oldPathReplaced); !os.IsNotExist(statErr) {
		t.Errorf("old file for the replaced track should be deleted from disk, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(oldPathUntouched); statErr != nil {
		t.Errorf("old file for the untouched track should survive untouched, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(newPath); statErr != nil {
		t.Errorf("new file should survive, stat err = %v", statErr)
	}

	after, err := musicStore.ListTrackFilesByAlbum(album.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("track files after swap = %+v, want 2 (new track-one file + untouched track-two file)", after)
	}
	for _, tf := range after {
		if tf.ID == oldTFReplaced.ID {
			t.Errorf("old track_files row for the replaced track should have been deleted: %+v", tf)
		}
	}
}
