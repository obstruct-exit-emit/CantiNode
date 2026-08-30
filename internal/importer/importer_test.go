package importer

import (
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/musicscanner"
	"github.com/cantinode/cantinode/internal/tagwriter"
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
// download's content) and a destination music root folder. The raw *sql.DB
// is returned alongside the stores that wrap it — needed by tests that must
// reach past download.Store's own exported API, e.g. backdating a grab's
// grabbed_at to simulate one that's been in flight a while (see
// TestPollOnceFailsGrabPastGracePeriod).
func setup(t *testing.T, sab *httptest.Server) (*Service, *download.Store, *musiclibrary.Store, string, *sql.DB) {
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
	scanner := musicscanner.New(musicStore, mb, nil, nil, "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}", 0.75, false, tagwriter.AllEnabled, false)

	cfg := &config.Config{}

	return New(downloads, scanner, musicStore, cfg), dlStore, musicStore, destRoot, db
}

// TestPollOnceImportsCompletedDownload proves the copy mechanics: the file
// lands on disk, the source is cleaned up, and the grab resolves imported.
// Deliberately not tied to a wanted album — this suite's fake MusicBrainz
// client points at nothing reachable (see setup), so a file here can never
// actually be matched, and a WantedAlbumID grab would (correctly, see
// TestPollOnceRevertsWantedAlbumWhenNothingMatches) never resolve imported
// under those conditions.
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
	svc, dlStore, _, destRoot, _ := setup(t, sab)

	if err := dlStore.AddGrab(&download.GrabRecord{
		ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
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

// TestPollOnceRevertsWantedAlbumWhenNothingMatches is the regression test
// for a real gap found live: a whole-disc rip (one giant file per CD side,
// never split into individual tracks) copies real audio data successfully,
// but nothing about it can be matched to the release it was grabbed for.
// The grab used to be marked imported and the wanted_albums row
// force-deleted regardless of whether the scan actually matched anything —
// the album silently vanished from Wanted while it never actually became
// owned, with nothing pointing back at the copied files sitting unmatched.
// A real match clears the wanted row itself (musicscanner.applyMatch's own
// ClearWantedAlbumByReleaseGroup); this suite's fake MusicBrainz client
// points at nothing reachable (see setup), so the copied file here can
// never be matched — exactly the condition that must now revert the wanted
// album instead of silently reporting success.
func TestPollOnceRevertsWantedAlbumWhenNothingMatches(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Test Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(albumDir, "disc1.flac"), []byte("whole disc rip"), 0o644); err != nil {
		t.Fatal(err)
	}

	sab, _ := mockSab(t, albumDir, "Completed")
	svc, dlStore, musicStore, _, _ := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatal(err)
	}
	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Test Album",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatal(err)
	}

	result := svc.PollOnce(t.Context())
	if result.Checked != 1 || result.Imported != 0 || result.Failed != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 checked, 0 imported, 1 failed", result)
	}

	// The wanted row must survive, reverted to "wanted" — not deleted as if
	// the album had actually been satisfied.
	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatalf("GetWantedAlbum after unmatched import: %v", err)
	}
	if got.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want %q", got.Status, musiclibrary.WantedStatusWanted)
	}

	grabs, err := dlStore.ListGrabs(download.GrabStatusFailed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("failed grabs = %+v, want exactly 1", grabs)
	}

	// Not blocklisted: copying real data but failing to match it could just
	// as easily be a transient miss as a genuinely unusable release.
	blocked, err := dlStore.BlockedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if download.IsBlocked(blocked, "", "Test Album") {
		t.Error("an unmatched-but-copied release must not be blocklisted — that's not confident evidence the release itself is bad")
	}
}

// TestPollOnceFailsGrabWithNoAudioFiles is the regression test for a real
// live bug found during a burn-in test: a completed download whose folder
// contains nothing copyTree recognizes as audio (a bad/mislabeled release,
// or a client-reported "completed" state that arrived before extraction
// actually finished) used to fall through to the success path anyway —
// resolving the grab as imported, deleting its wanted_albums row (so the
// album could never be automatically retried), and deleting the "completed"
// download's data — for content that was never actually added to the
// library. Confirmed live: grabs.status ended up 'imported' and the
// wanted_albums row was gone, but no album/track/file existed anywhere.
// copyTree returning an empty, non-error result must now be treated as a
// real failure: the grab fails, the wanted album reverts to "wanted" (same
// as any other failed grab), and the source is left alone in the download
// client rather than being discarded.
func TestPollOnceFailsGrabWithNoAudioFiles(t *testing.T) {
	src := t.TempDir()
	albumDir := filepath.Join(src, "Bad Release")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only a non-audio file — copyTree filters by extension, so this leaves
	// it with nothing to copy, the exact condition that triggered the bug.
	if err := os.WriteFile(filepath.Join(albumDir, "readme.txt"), []byte("nothing here"), 0o644); err != nil {
		t.Fatal(err)
	}

	sab, deleteCalls := mockSab(t, albumDir, "Completed")
	svc, dlStore, musicStore, destRoot, _ := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Bad Release", "Album", "2020")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatalf("set wanted album downloading: %v", err)
	}

	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Bad Release",
		Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatalf("seed grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Checked != 1 || result.Imported != 0 || result.Failed != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 checked, 0 imported, 1 failed", result)
	}

	if _, err := os.Stat(filepath.Join(destRoot, "Bad Release")); !os.IsNotExist(err) {
		t.Errorf("nothing should have landed in the library — stat err = %v", err)
	}

	grabs, err := dlStore.ListGrabs(download.GrabStatusFailed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("failed grabs = %+v, want exactly 1", grabs)
	}

	// The wanted album must revert to "wanted" (retryable), not be deleted
	// as if it had actually been satisfied.
	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatalf("GetWantedAlbum after failed import: %v", err)
	}
	if got.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want %q", got.Status, musiclibrary.WantedStatusWanted)
	}

	if *deleteCalls != 0 {
		t.Error("importer must not remove the download from its client for a failed import")
	}
	if _, err := os.Stat(albumDir); err != nil {
		t.Errorf("source directory should be left alone for inspection, stat err = %v", err)
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
	svc, dlStore, _, destRoot, _ := setup(t, sab)

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
	svc, dlStore, _, destRoot, _ := setup(t, sab)
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
	svc, dlStore, musicStore, _, _ := setup(t, sab)

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

// TestPollOnceBlocklistsGenuineReleaseFailure is the regression test for a
// real gap found live: nothing in this codebase ever actually wrote to the
// blocklist outside of a test seeding one directly — candidatesearch's own
// filtering (keeping blocklisted releases out of future search results)
// was fully built and tested, but the write side that's supposed to
// populate it never existed. A download the client itself reports as
// failed is real evidence the release is bad, so it must now land in the
// blocklist.
func TestPollOnceBlocklistsGenuineReleaseFailure(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Failed")
	svc, dlStore, musicStore, _, _ := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatal(err)
	}
	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Bad Release",
		GUID: "guid-bad-release", Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatal(err)
	}

	if result := svc.PollOnce(t.Context()); result.Failed != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 failed", result)
	}

	blocked, err := dlStore.BlockedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !download.IsBlocked(blocked, "guid-bad-release", "Bad Release") {
		t.Error("a release the download client itself reported as failed should be blocklisted")
	}
}

// TestPollOnceVanishedGrabIsNotBlocklisted confirms an environmental
// failure (the grab simply vanished from the client's queue) never
// blocklists the release — the client losing track of a download proves
// nothing about the release's own quality, the same reasoning
// handleRemoveQueueItem already applies to its own "not blocklisted" case.
func TestPollOnceVanishedGrabIsNotBlocklisted(t *testing.T) {
	sab := mockSabEmpty(t)
	svc, dlStore, musicStore, _, db := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	wanted, err := musicStore.GetOrCreateWantedAlbum(artist.ID, "rg-mbid", "Test Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	if err := musicStore.SetWantedAlbumStatus(wanted.ID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatal(err)
	}
	if err := dlStore.AddGrab(&download.GrabRecord{
		WantedAlbumID: wanted.ID, ClientConfigID: 1, ClientItemID: "nzo1", Title: "Good Release",
		GUID: "guid-good-release", Protocol: download.ProtocolUsenet, MediaType: "music",
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * grabVanishedGrace).Format(time.DateTime)
	if _, err := db.Exec(`UPDATE grabs SET grabbed_at = ? WHERE client_item_id = 'nzo1'`, old); err != nil {
		t.Fatal(err)
	}

	if result := svc.PollOnce(t.Context()); result.Failed != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 failed", result)
	}

	blocked, err := dlStore.BlockedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if download.IsBlocked(blocked, "guid-good-release", "Good Release") {
		t.Error("a grab that just vanished from the client's queue must not blocklist the release — that's environmental, not evidence the release itself is bad")
	}
}

func TestPollOnceIgnoresGrabsStillInProgress(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Downloading")
	svc, dlStore, _, _, _ := setup(t, sab)

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

// mockSabEmpty fakes a client whose queue and history are both genuinely
// empty — no slot with any nzo_id at all — the "this item isn't here"
// half of the vanished-grab scenarios below, as opposed to mockSab, which
// always reports exactly one slot for "nzo1".
func mockSabEmpty(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "queue":
			w.Write([]byte(`{"queue":{"slots":[]}}`))
		case "history":
			w.Write([]byte(`{"history":{"slots":[]}}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPollOnceGivesFreshGrabGraceBeforeFailing is the regression test for a
// real, live gap: a grab genuinely not (yet) in its client's queue used to
// be failed and reverted to "wanted" on the very first miss — found live
// against a real TorBox/SABnzbd bridge, which can take longer than one
// PollInterval to actually list an item it already accepted, wrongly
// failing a perfectly healthy download within a minute of grabbing it (see
// grabVanishedGrace's own doc comment). A grab still younger than
// grabVanishedGrace must be left alone (still "grabbed") when its client
// item id isn't found, not immediately failed.
func TestPollOnceGivesFreshGrabGraceBeforeFailing(t *testing.T) {
	sab := mockSabEmpty(t)
	svc, dlStore, musicStore, _, _ := setup(t, sab)

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
	// AddGrab stamps grabbed_at as "now" (the default this test relies on),
	// so this grab is as fresh as it can be — well within grabVanishedGrace.

	result := svc.PollOnce(t.Context())
	if result.Failed != 0 {
		t.Errorf("PollOnce result = %+v, want 0 failed — a fresh grab must get grace, not an immediate fail", result)
	}
	grabs, err := dlStore.ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 {
		t.Fatalf("still-grabbed grabs = %+v, want exactly 1 untouched", grabs)
	}
	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != musiclibrary.WantedStatusDownloading {
		t.Errorf("wanted album status = %q, want it to stay %q while the grab is still within its grace period", got.Status, musiclibrary.WantedStatusDownloading)
	}
}

// TestPollOnceFailsGrabPastGracePeriod confirms grabVanishedGrace only
// delays a genuine failure, it doesn't suppress it: a grab old enough that
// even a slow bridge should have listed it by now still gets failed and
// its wanted album reverted, the same as before this grace period existed.
func TestPollOnceFailsGrabPastGracePeriod(t *testing.T) {
	sab := mockSabEmpty(t)
	svc, dlStore, musicStore, _, db := setup(t, sab)

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
	// Backdate past grabVanishedGrace — AddGrab itself always stamps "now"
	// (grabbed_at's own DB default), so simulating an old grab needs a
	// direct update afterward.
	old := time.Now().UTC().Add(-2 * grabVanishedGrace).Format(time.DateTime)
	if _, err := db.Exec(`UPDATE grabs SET grabbed_at = ? WHERE client_item_id = 'nzo1'`, old); err != nil {
		t.Fatalf("backdate grab: %v", err)
	}

	result := svc.PollOnce(t.Context())
	if result.Failed != 1 {
		t.Errorf("PollOnce result = %+v, want 1 failed — a grab this old must not get indefinite grace", result)
	}
	got, err := musicStore.GetWantedAlbum(wanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want %q", got.Status, musiclibrary.WantedStatusWanted)
	}
}

func TestPollOnceNoGrabsIsANoop(t *testing.T) {
	sab, _ := mockSab(t, "/does/not/matter", "Completed")
	svc, _, _, _, _ := setup(t, sab)

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
	if len(copied) != 2 {
		t.Errorf("copied = %d, want 2", len(copied))
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
	if len(copied) != 2 {
		t.Errorf("copied = %d, want 2 (the two audio files)", len(copied))
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
	if err := os.WriteFile(filepath.Join(albumDir, "readme.flac"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// mockSab's history slot always reports nzo_id "nzo1" (lowercase); the
	// grab below is recorded with the uppercase form on purpose.
	sab, _ := mockSab(t, albumDir, "Completed")
	svc, dlStore, _, _, _ := setup(t, sab)

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
	svc, _, musicStore, root, _ := setup(t, sab)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "album-mbid", "rg-mbid", "Test Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	trackReplaced, err := musicStore.GetOrCreateTrack(album.ID, "track-replaced-mbid", "Track One", 1, 1, 200000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	trackUntouched, err := musicStore.GetOrCreateTrack(album.ID, "track-untouched-mbid", "Track Two", 2, 1, 200000, "", "", "")
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
