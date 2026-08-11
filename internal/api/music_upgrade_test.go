package api

import (
	"fmt"
	"net/http"
	"testing"
)

// seedOwnedAlbum inserts an artist, an owned album, one track, and one
// track_file recorded at format — enough for handleSearchAlbumUpgrade to
// compute "what's already owned" without a real scan/match pass. Returns
// the album id.
func seedOwnedAlbum(t *testing.T, a *testAPI, format string) int64 {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, t.TempDir())
	if err != nil {
		t.Fatalf("insert root folder: %v", err)
	}
	rootFolderID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO artists (mbid, name, sort_name) VALUES (?, ?, ?)`,
		"artist-upgrade-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	artistID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title) VALUES (?, ?, ?, ?)`,
		artistID, "album-upgrade-mbid", "rg-geogaddi", "Geogaddi")
	if err != nil {
		t.Fatalf("insert album: %v", err)
	}
	albumID, _ := res.LastInsertId()

	res, err = a.db.Exec(`INSERT INTO tracks (album_id, mbid, title, track_number) VALUES (?, ?, ?, ?)`,
		albumID, "track-upgrade-mbid", "Alpha and Omega", 1)
	if err != nil {
		t.Fatalf("insert track: %v", err)
	}
	trackID, _ := res.LastInsertId()

	if _, err := a.db.Exec(`INSERT INTO track_files (root_folder_id, track_id, path, format, match_status) VALUES (?, ?, ?, ?, 'matched')`,
		rootFolderID, trackID, "/music/owned."+format, format); err != nil {
		t.Fatalf("insert track file: %v", err)
	}
	return albumID
}

func setUpgradesAllowed(t *testing.T, a *testAPI, allowed bool, cutoff string) {
	t.Helper()
	if _, err := a.db.Exec(`UPDATE quality_profiles SET upgrades_allowed = ?, cutoff = ? WHERE media_type = 'music' AND is_default = 1`,
		allowed, cutoff); err != nil {
		t.Fatalf("set upgrades allowed: %v", err)
	}
}

// TestSearchAlbumUpgradeRequiresUpgradesAllowed is the regression test for
// the actual gap this closes: internal/release's MinFormatScore/upgrade
// rejection logic already existed, but nothing in the app ever called a
// search with an owned format to check against — the toggle in Settings
// looked live but had no effect. This confirms the new endpoint refuses
// outright while the profile's own "Allow upgrades" is off, the plainest
// case a caller could get wrong.
func TestSearchAlbumUpgradeRequiresUpgradesAllowed(t *testing.T) {
	a := newTestAPI(t)
	albumID := seedOwnedAlbum(t, a, "mp3")

	resp := a.call("GET", fmt.Sprintf("/api/v1/music/album/%d/upgrade/search", albumID), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (upgrades not allowed)", resp.StatusCode)
	}
}

// TestSearchAlbumUpgradeRejectsAtCutoff confirms an album already at (or
// above) the quality profile's cutoff format is refused outright, rather
// than burning an indexer search for nothing.
func TestSearchAlbumUpgradeRejectsAtCutoff(t *testing.T) {
	a := newTestAPI(t)
	setUpgradesAllowed(t, a, true, "") // empty cutoff = the profile's best format
	albumID := seedOwnedAlbum(t, a, "flac")

	resp := a.call("GET", fmt.Sprintf("/api/v1/music/album/%d/upgrade/search", albumID), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (already at cutoff)", resp.StatusCode)
	}
}

// TestSearchAlbumUpgradeApprovesOnlyGenuineUpgrades is the core wiring
// proof: an album owned in mp3, upgrades allowed, cutoff at the profile's
// best format (flac) — the FLAC release found must approve (it's strictly
// better than the owned mp3), exercising release.Preferences.MinFormatScore
// for the first time anywhere in the running app.
func TestSearchAlbumUpgradeApprovesOnlyGenuineUpgrades(t *testing.T) {
	a := newTestAPI(t)
	setUpgradesAllowed(t, a, true, "")
	albumID := seedOwnedAlbum(t, a, "mp3")

	mock := mockTorznabIndexer(t, musicSearchXML)
	a.want(a.call("POST", "/api/v1/indexer", map[string]any{
		"name": "Mock", "type": "torznab", "baseUrl": mock.URL, "enabled": true,
	}, nil), http.StatusCreated)

	var resp struct {
		Releases []struct {
			GUID     string `json:"guid"`
			Approved bool   `json:"approved"`
		} `json:"releases"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/album/%d/upgrade/search", albumID), nil, &resp), http.StatusOK)

	found := false
	for _, r := range resp.Releases {
		if r.GUID == "https://mock/torrent/good" {
			found = true
			if !r.Approved {
				t.Errorf("FLAC release over an owned mp3 should approve as an upgrade: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("releases = %+v, want the FLAC release present", resp.Releases)
	}
}

// TestSearchAlbumUpgradeRejectsUnrecognizedOwnedFormat is the regression
// test for a real bug: when none of an album's owned track-file formats
// matched the quality profile's own format list, "current best score"
// silently stayed 0 — and since MinFormatScore's rejection rule only fires
// when it's > 0, that 0 disabled the upgrade-only gate entirely rather
// than refusing outright. An unrecognized owned format can't be compared
// against anything, so this must now be a clean 400, not a search that
// runs anyway with the gate silently switched off.
func TestSearchAlbumUpgradeRejectsUnrecognizedOwnedFormat(t *testing.T) {
	a := newTestAPI(t)
	setUpgradesAllowed(t, a, true, "")
	// "wav" isn't in DefaultMusicPreferences' FormatScores (flac/wav/mp3/m4a/opus
	// — wait, wav *is* default; use a format guaranteed absent instead).
	albumID := seedOwnedAlbum(t, a, "wma")

	resp := a.call("GET", fmt.Sprintf("/api/v1/music/album/%d/upgrade/search", albumID), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (owned format not recognized by the profile)", resp.StatusCode)
	}
}

// TestGrabAlbumUpgradeRequiresDownloadClient confirms the grab endpoint is
// wired to the same client-routing as the wanted-album grab, without
// needing a wanted_albums row: no client configured must fail cleanly
// rather than panicking on the wantedAlbumID=0 path. Upgrades must be
// allowed first — otherwise the eligibility gate (see the next test)
// refuses the request before ever reaching client routing.
func TestGrabAlbumUpgradeRequiresDownloadClient(t *testing.T) {
	a := newTestAPI(t)
	setUpgradesAllowed(t, a, true, "")
	albumID := seedOwnedAlbum(t, a, "mp3")

	resp := a.call("POST", fmt.Sprintf("/api/v1/music/album/%d/upgrade/grab", albumID), map[string]any{
		"title": "Boards of Canada - Geogaddi FLAC", "downloadUrl": "https://mock/dl/good.torrent",
		"protocol": "torrent", "guid": "https://mock/torrent/good",
	}, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no download client configured)", resp.StatusCode)
	}
}

// TestGrabAlbumUpgradeRejectsWhenUpgradesDisabled is the regression test for
// the actual gap this closes: handleGrabAlbumUpgrade used to trust its
// caller entirely — no re-check of "Allow upgrades" or the cutoff — so a
// grab request could bypass the exact toggle handleSearchAlbumUpgrade
// enforces (settings changed after the search, or a caller skipping the
// search step outright). With upgrades left off, a grab must fail the same
// way a search would, before ever touching a download client.
func TestGrabAlbumUpgradeRejectsWhenUpgradesDisabled(t *testing.T) {
	a := newTestAPI(t)
	albumID := seedOwnedAlbum(t, a, "mp3") // upgrades not allowed by default

	resp := a.call("POST", fmt.Sprintf("/api/v1/music/album/%d/upgrade/grab", albumID), map[string]any{
		"title": "Boards of Canada - Geogaddi FLAC", "downloadUrl": "https://mock/dl/good.torrent",
		"protocol": "torrent", "guid": "https://mock/torrent/good",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (upgrades not allowed) — grab must not bypass the eligibility gate", resp.StatusCode)
	}
}
