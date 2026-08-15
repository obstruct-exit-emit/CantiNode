package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// TestSuggestTrackFileMatchesRequiresFields covers request validation only
// — handleSuggestTrackFileMatches' actual matching logic (SuggestMatches)
// is exercised directly against real ReleaseWithTracklist fixtures in
// internal/musicscanner's own tests, the same boundary this codebase's
// other MusicBrainz-backed handlers (manual match, tracklist preview) are
// already tested at, since newTestAPI wires a real (network-reaching)
// MusicBrainz client with no local mock injection point.
func TestSuggestTrackFileMatchesRequiresFields(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", "/api/v1/music/trackfile/match-suggest", map[string]any{}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/music/trackfile/match-suggest",
		map[string]any{"fileIds": []int64{1}}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/music/trackfile/match-suggest",
		map[string]any{"releaseGroupMbid": "rg-mbid"}, nil), http.StatusBadRequest)
}

// TestQuickAddMusicArtistRequiresMbid covers request validation only —
// same boundary as TestSuggestTrackFileMatchesRequiresFields above, for
// the same reason (a real, network-reaching MusicBrainz client with no
// local mock injection point at this layer).
func TestQuickAddMusicArtistRequiresMbid(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", "/api/v1/music/artist/quick", map[string]any{}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/music/artist/quick", map[string]any{"mbid": ""}, nil), http.StatusBadRequest)
}

// TestHasRealVersionMetadata is the regression test for a real bug: a
// migrated placeholder row (release_mbid/title only, from migration 022's
// carryover of the old single-tracklist-cache scheme, marked fetched=0 by
// migration 023) must not be treated as "already fully cached," or
// handleListReleaseGroupVersions would never re-fetch the real version list
// for any pre-existing artist. hasRealVersionMetadata now trusts the
// explicit Fetched flag rather than guessing from TrackCount/Status, so a
// genuinely-fetched row with sparse field data (some real MusicBrainz
// releases have neither field populated) must still count as real.
func TestHasRealVersionMetadata(t *testing.T) {
	cases := []struct {
		name string
		in   []musiclibrary.ReleaseGroupVersion
		want bool
	}{
		{"empty", nil, false},
		{"migrated placeholder only", []musiclibrary.ReleaseGroupVersion{
			{ReleaseMBID: "rel-1", Title: "Album", Fetched: false},
		}, false},
		{"genuinely fetched", []musiclibrary.ReleaseGroupVersion{
			{ReleaseMBID: "rel-1", Title: "Album", Status: "Official", TrackCount: 10, Fetched: true},
		}, true},
		{"one placeholder, one real", []musiclibrary.ReleaseGroupVersion{
			{ReleaseMBID: "rel-1", Title: "Album", Fetched: false},
			{ReleaseMBID: "rel-2", Title: "Album", TrackCount: 5, Fetched: true},
		}, true},
		{"genuinely fetched but sparse fields", []musiclibrary.ReleaseGroupVersion{
			{ReleaseMBID: "rel-1", Title: "Album", Fetched: true},
		}, true},
	}
	for _, c := range cases {
		if got := hasRealVersionMetadata(c.in); got != c.want {
			t.Errorf("%s: hasRealVersionMetadata = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestWantMusicAlbumRefusesAlreadyOwned is the API-level counterpart to
// musiclibrary's TestGetOrCreateWantedAlbumRefusesAlreadyOwned — confirms
// the handler surfaces ErrAlreadyOwned as a clean 400 instead of a 500 or
// (worse) silently creating a duplicate owned+wanted library-grid entry.
func TestWantMusicAlbumRefusesAlreadyOwned(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if err := musicStore.ReplaceArtistReleaseGroups(artist.ID, []musiclibrary.ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Album One", PrimaryType: "Album", FirstReleaseDate: "2020"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}
	if _, err := musicStore.GetOrCreateAlbum(artist.ID, "rel-1", "rg-1", "Album One", "2020", "Album"); err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}

	a.want(a.call("POST", fmt.Sprintf("/api/v1/music/artist/%d/wanted", artist.ID),
		map[string]any{"releaseGroupMbid": "rg-1"}, nil), http.StatusBadRequest)

	wanted, err := musicStore.ListWantedAlbumsByArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wanted) != 0 {
		t.Errorf("wanted albums = %+v, want empty — no duplicate should have been created", wanted)
	}
}

// TestRemoveMusicArtistPurgesReleaseGroupCache is the regression test for
// "if an artist is removed, its cached metadata should be deleted since
// the artist is no longer in the library" — release_group_versions and
// release_tracklist_cache rows for the removed artist's release groups
// must actually disappear, not just the artist/albums/tracks rows
// DeleteArtist's own FK cascade already covers. Doesn't touch MusicBrainz
// at all (purgeArtistCaches is pure DB + local disk), so this is safe to
// test at the full HTTP-handler level.
func TestRemoveMusicArtistPurgesReleaseGroupCache(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if err := musicStore.ReplaceArtistReleaseGroups(artist.ID, []musiclibrary.ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Album One"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}
	if err := musicStore.ReplaceReleaseGroupVersions("rg-1", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-1", Title: "Album One", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}
	if err := musicStore.SetCachedTracklist("rel-1", "rg-1", `[]`); err != nil {
		t.Fatalf("SetCachedTracklist: %v", err)
	}

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artist.ID), nil, nil), http.StatusOK)

	if versions, err := musicStore.ListReleaseGroupVersions("rg-1"); err != nil || len(versions) != 0 {
		t.Errorf("versions after artist removal = %+v, err %v, want empty", versions, err)
	}
	if _, err := musicStore.GetCachedTracklist("rel-1"); !errors.Is(err, musiclibrary.ErrNotFound) {
		t.Errorf("tracklist after artist removal: err = %v, want ErrNotFound", err)
	}
}

// TestRemoveMusicArtistKeepsSharedReleaseGroupCache is the regression test
// for a real bug: a release group can legitimately be cached under more
// than one artist's own discography (a collaboration/split release both
// artists' own MusicBrainz pages list) — artist_release_groups is unique
// per (artist_id, release_group_mbid), not per release_group_mbid alone.
// Removing one artist that references a shared release group must not wipe
// its cached version/tracklist metadata out from under a different,
// still-present artist that references the very same release group.
func TestRemoveMusicArtistKeepsSharedReleaseGroupCache(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)

	artistA, err := musicStore.GetOrCreateArtist("artist-a-mbid", "Artist A", "Artist A")
	if err != nil {
		t.Fatalf("GetOrCreateArtist A: %v", err)
	}
	artistB, err := musicStore.GetOrCreateArtist("artist-b-mbid", "Artist B", "Artist B")
	if err != nil {
		t.Fatalf("GetOrCreateArtist B: %v", err)
	}
	// Both artists' own discography sync pulled in the same shared release
	// group (a split/collaboration release) — this is the scenario that
	// distinguishes "no one references it anymore" from "the artist being
	// removed happened to reference it."
	for _, artistID := range []int64{artistA.ID, artistB.ID} {
		if err := musicStore.ReplaceArtistReleaseGroups(artistID, []musiclibrary.ReleaseGroupCache{
			{ReleaseGroupMBID: "rg-shared", Title: "Split Release"},
		}); err != nil {
			t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
		}
	}
	if err := musicStore.ReplaceReleaseGroupVersions("rg-shared", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-shared", ReleaseMBID: "rel-shared", Title: "Split Release", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}
	if err := musicStore.SetCachedTracklist("rel-shared", "rg-shared", `[]`); err != nil {
		t.Fatalf("SetCachedTracklist: %v", err)
	}

	// Remove artist A only — artist B still references rg-shared.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artistA.ID), nil, nil), http.StatusOK)

	if versions, err := musicStore.ListReleaseGroupVersions("rg-shared"); err != nil || len(versions) != 1 {
		t.Errorf("versions after removing artist A = %+v, err %v, want still 1 (artist B still references it)", versions, err)
	}
	if _, err := musicStore.GetCachedTracklist("rel-shared"); err != nil {
		t.Errorf("tracklist after removing artist A: err = %v, want still cached (artist B still references it)", err)
	}

	// Now remove artist B too — nobody references rg-shared anymore, so it
	// should finally be purged.
	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artistB.ID), nil, nil), http.StatusOK)

	if versions, err := musicStore.ListReleaseGroupVersions("rg-shared"); err != nil || len(versions) != 0 {
		t.Errorf("versions after removing both artists = %+v, err %v, want empty", versions, err)
	}
	if _, err := musicStore.GetCachedTracklist("rel-shared"); !errors.Is(err, musiclibrary.ErrNotFound) {
		t.Errorf("tracklist after removing both artists: err = %v, want ErrNotFound", err)
	}
}

// TestRemoveMusicArtistPurgesOwnedAlbumCoverArtEvenWithoutCachedVersions is
// the regression test for a real gap found in review: purgeArtistCaches
// only collected release MBIDs to purge cover art for from
// release_group_versions — but an artist can be removed before its
// background discography-version sweep (cacheDiscographyVersions, which
// can take minutes to hours) has cached anything for a release group at
// all, in which case that lookup returns nothing and an owned album's
// cover art survived removal, contradicting handleRemoveMusicArtist's own
// documented "purges every piece of cached metadata" guarantee. Owned
// albums' own MBIDs must always be purged regardless.
func TestRemoveMusicArtistPurgesOwnedAlbumCoverArtEvenWithoutCachedVersions(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	trackPath := filepath.Join(rootDir, "Boards of Canada", "Geogaddi", "01 - Ready Lets Go.flac")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	artistID, albumID, _ := seedMusicFixture(t, a, rf.ID, trackPath)
	var album struct {
		MBID string `json:"mbid"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/album/%d", albumID), nil, &album), http.StatusOK)

	// Deliberately do NOT seed release_group_versions for this album's
	// release group — simulates an artist removed before the background
	// version-cache sweep (which can take minutes to hours) ever reached
	// it.
	coverPath := a.coverArtPath(album.MBID, ".jpg")
	if err := os.MkdirAll(filepath.Dir(coverPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverPath, []byte("fake cover art"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/artist/%d", artistID), nil, nil), http.StatusOK)

	if _, err := os.Stat(coverPath); !os.IsNotExist(err) {
		t.Errorf("cover art at %s still exists after artist removal (err = %v), want it purged", coverPath, err)
	}
}

// TestRemoveMusicAlbumKeepsReleaseGroupCache locks in the flip side: an
// album removal must NOT purge the artist's cached release-group metadata
// — the artist (and the rest of its discography) is still in the library,
// unlike a full artist removal.
func TestRemoveMusicAlbumKeepsReleaseGroupCache(t *testing.T) {
	a := newTestAPI(t)
	musicStore := musiclibrary.NewStore(a.db)

	artist, err := musicStore.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	album, err := musicStore.GetOrCreateAlbum(artist.ID, "rel-1", "rg-1", "Album One", "2020", "Album")
	if err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	if err := musicStore.ReplaceReleaseGroupVersions("rg-1", []musiclibrary.ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-1", Title: "Album One", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}
	if err := musicStore.SetCachedTracklist("rel-1", "rg-1", `[]`); err != nil {
		t.Fatalf("SetCachedTracklist: %v", err)
	}

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/music/album/%d", album.ID), nil, nil), http.StatusOK)

	if versions, err := musicStore.ListReleaseGroupVersions("rg-1"); err != nil || len(versions) != 1 {
		t.Errorf("versions after album removal = %+v, err %v, want still 1", versions, err)
	}
	if _, err := musicStore.GetCachedTracklist("rel-1"); err != nil {
		t.Errorf("tracklist after album removal: err = %v, want still cached", err)
	}
}

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
