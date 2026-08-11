package autosearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// musicSearchXML mirrors internal/api/music_test.go's own fixture: one
// clean FLAC release (approved), one naming an executable (spam, rejected
// outright by internal/relname), and one dead torrent (0 seeders,
// rejected) — enough to prove PollOnce picks the single approved survivor
// and ignores the rest.
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
</channel>
</rss>`

// noApprovedXML has only rejects — used to prove a sweep with nothing good
// enough grabs nothing and leaves the album wanted.
const noApprovedXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
<channel>
  <item>
    <title>Boards of Canada - Geogaddi Dead Torrent</title>
    <guid>https://mock/torrent/dead-only</guid>
    <link>https://mock/dl/dead-only.torrent</link>
    <torznab:attr name="size" value="400000000"/>
    <torznab:attr name="seeders" value="0"/>
    <torznab:attr name="peers" value="0"/>
  </item>
</channel>
</rss>`

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

// mockQbit fakes just enough of qBittorrent's Web API v2 for a grab to
// succeed: login, category creation, and add.
func mockQbit(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/createCategory":
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/add":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// testDeps bundles everything PollOnce needs, all backed by one throwaway
// SQLite database.
type testDeps struct {
	music     *musiclibrary.Store
	indexers  *indexer.Service
	downloads *download.Service
	store     *library.Store
}

func newTestDeps(t *testing.T) testDeps {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return testDeps{
		music:     musiclibrary.NewStore(db),
		indexers:  indexer.NewService(indexer.NewStore(db)),
		downloads: download.NewService(download.NewStore(db)),
		store:     library.NewStore(db),
	}
}

func (d testDeps) addIndexer(t *testing.T, searchXML string) {
	t.Helper()
	srv := mockTorznabIndexer(t, searchXML)
	if err := d.indexers.Store().Add(&indexer.Indexer{
		Name: "Mock", Type: indexer.TypeTorznab, BaseURL: srv.URL, Enabled: true, Priority: 1,
	}); err != nil {
		t.Fatalf("add indexer: %v", err)
	}
}

func (d testDeps) addQbit(t *testing.T) {
	t.Helper()
	srv := mockQbit(t)
	if err := d.downloads.Store().Add(&download.ClientConfig{
		Name: "qbit", Type: download.TypeQBittorrent, Host: srv.URL,
		Category: "cantinode", Enabled: true, Priority: 1,
	}); err != nil {
		t.Fatalf("add download client: %v", err)
	}
}

// seedWantedAlbum creates an artist (monitored per the monitored arg) and
// one wanted album under it, returning both ids.
func seedWantedAlbum(t *testing.T, d testDeps, mbidSuffix string, monitored bool) (artistID, wantedID int64) {
	t.Helper()
	artist, err := d.music.GetOrCreateArtist("artist-"+mbidSuffix, "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	if monitored {
		if err := d.music.SetArtistMonitored(artist.ID, true); err != nil {
			t.Fatalf("monitor artist: %v", err)
		}
	}
	wanted, err := d.music.GetOrCreateWantedAlbum(artist.ID, "rg-"+mbidSuffix, "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("seed wanted album: %v", err)
	}
	return artist.ID, wanted.ID
}

func TestPollOnceGrabsBestApprovedReleaseForMonitoredArtist(t *testing.T) {
	d := newTestDeps(t)
	d.addIndexer(t, musicSearchXML)
	d.addQbit(t)
	_, wantedID := seedWantedAlbum(t, d, "grab", true)

	result := New(d.music, d.indexers, d.downloads, d.store).PollOnce(context.Background())
	if result.Checked != 1 || result.Grabbed != 1 {
		t.Fatalf("PollOnce result = %+v, want 1 checked, 1 grabbed", result)
	}

	wanted, err := d.music.GetWantedAlbum(wantedID)
	if err != nil {
		t.Fatal(err)
	}
	if wanted.Status != musiclibrary.WantedStatusDownloading {
		t.Errorf("wanted album status = %q, want %q", wanted.Status, musiclibrary.WantedStatusDownloading)
	}

	grabs, err := d.downloads.Store().ListGrabs(download.GrabStatusGrabbed)
	if err != nil {
		t.Fatal(err)
	}
	if len(grabs) != 1 || grabs[0].WantedAlbumID != wantedID {
		t.Fatalf("grabs = %+v, want exactly one tied to wanted album %d", grabs, wantedID)
	}
}

func TestPollOnceSkipsUnmonitoredArtist(t *testing.T) {
	d := newTestDeps(t)
	d.addIndexer(t, musicSearchXML)
	d.addQbit(t)
	_, wantedID := seedWantedAlbum(t, d, "unmon", false)

	result := New(d.music, d.indexers, d.downloads, d.store).PollOnce(context.Background())
	if result.Checked != 0 || result.Grabbed != 0 {
		t.Fatalf("PollOnce result = %+v, want nothing touched for an unmonitored artist", result)
	}

	wanted, err := d.music.GetWantedAlbum(wantedID)
	if err != nil {
		t.Fatal(err)
	}
	if wanted.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want unchanged %q", wanted.Status, musiclibrary.WantedStatusWanted)
	}
}

func TestPollOnceSkipsAlreadyDownloadingAlbum(t *testing.T) {
	d := newTestDeps(t)
	d.addIndexer(t, musicSearchXML)
	d.addQbit(t)
	_, wantedID := seedWantedAlbum(t, d, "dl", true)
	if err := d.music.SetWantedAlbumStatus(wantedID, musiclibrary.WantedStatusDownloading); err != nil {
		t.Fatal(err)
	}

	result := New(d.music, d.indexers, d.downloads, d.store).PollOnce(context.Background())
	if result.Checked != 0 || result.Grabbed != 0 {
		t.Fatalf("PollOnce result = %+v, want an already-downloading album left alone", result)
	}
}

func TestPollOnceGrabsNothingWhenNoApprovedRelease(t *testing.T) {
	d := newTestDeps(t)
	d.addIndexer(t, noApprovedXML)
	d.addQbit(t)
	_, wantedID := seedWantedAlbum(t, d, "none", true)

	result := New(d.music, d.indexers, d.downloads, d.store).PollOnce(context.Background())
	if result.Checked != 1 || result.Grabbed != 0 {
		t.Fatalf("PollOnce result = %+v, want 1 checked, 0 grabbed (nothing approved)", result)
	}

	wanted, err := d.music.GetWantedAlbum(wantedID)
	if err != nil {
		t.Fatal(err)
	}
	if wanted.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want it to stay %q for the next sweep", wanted.Status, musiclibrary.WantedStatusWanted)
	}
}

func TestPollOnceRespectsBlocklist(t *testing.T) {
	d := newTestDeps(t)
	d.addIndexer(t, musicSearchXML)
	d.addQbit(t)
	_, wantedID := seedWantedAlbum(t, d, "blocked", true)
	if err := d.downloads.Store().AddBlock("https://mock/torrent/good", "Boards of Canada - Geogaddi FLAC", "test"); err != nil {
		t.Fatalf("seed blocklist: %v", err)
	}

	result := New(d.music, d.indexers, d.downloads, d.store).PollOnce(context.Background())
	// The only approved candidate is blocklisted, and no other candidate in
	// musicSearchXML approves — nothing left to grab.
	if result.Grabbed != 0 {
		t.Fatalf("PollOnce result = %+v, want the blocklisted release never grabbed", result)
	}
	wanted, err := d.music.GetWantedAlbum(wantedID)
	if err != nil {
		t.Fatal(err)
	}
	if wanted.Status != musiclibrary.WantedStatusWanted {
		t.Errorf("wanted album status = %q, want unchanged %q", wanted.Status, musiclibrary.WantedStatusWanted)
	}
}

// TestRunPeriodicRepeatsUntilCanceled proves RunPeriodic actually loops
// (calling the schedule function fresh each time, per the config-driven
// "daily" mode's own self-correcting design) rather than firing once and
// stopping, and that it stops promptly once ctx is done.
func TestRunPeriodicRepeatsUntilCanceled(t *testing.T) {
	d := newTestDeps(t)
	s := New(d.music, d.indexers, d.downloads, d.store)

	var calls int32
	next := func(now time.Time) time.Time {
		atomic.AddInt32(&calls, 1)
		return now.Add(2 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s.RunPeriodic(ctx, next)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("next called %d time(s) in 50ms of 2ms cycles, want at least 2 — RunPeriodic should keep looping until ctx is done", got)
	}
}

// TestRunPeriodicNilScheduleFallsBackAndStopsOnCancel: a nil schedule
// function must not panic (falls back to a plain PollInterval ticker), and
// — the actually load-bearing assertion — a canceled context must stop the
// wait immediately rather than sitting through the full 24h fallback.
func TestRunPeriodicNilScheduleFallsBackAndStopsOnCancel(t *testing.T) {
	d := newTestDeps(t)
	s := New(d.music, d.indexers, d.downloads, d.store)

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		s.RunPeriodic(ctx, nil)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPeriodic did not return promptly after ctx was canceled")
	}
}
