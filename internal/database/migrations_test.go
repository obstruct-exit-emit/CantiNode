package database

import (
	"database/sql"
	"testing"
	"time"
)

// TestMigration0003DedupesExistingAlbumsByReleaseGroup simulates
// upgrading a database that already has the pre-fix bug's damage: two
// albums rows for the same (artist_id, release_group_mbid) — e.g. two
// different pressings of "Layla and Other Assorted Love Songs" that each
// got their own row because GetOrCreateAlbum used to dedupe on mbid
// alone. 0003_album_release_group_unique.sql must collapse those down to
// one row (keeping the lowest id) before it creates the unique index,
// otherwise the index's own creation would fail on a real installation
// that hit this bug. A genuinely distinct release group (e.g. an
// unrelated compilation that a fuzzy match wrongly attached a track to)
// must be left alone — this migration only merges true duplicates.
func TestMigration0003DedupesExistingAlbumsByReleaseGroup(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
	db := &DB{DB: sqlDB}

	// Apply only 0001/0002 directly, to reach the schema as it existed
	// right before this fix — i.e. before the unique index exists, so
	// the duplicate insert below is even possible.
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_acquisition.sql"} {
		b, err := migrationsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO artists (id, mbid, name, sort_name, created_at, updated_at) VALUES (1, 'artist-mbid', 'Derek and the Dominos', 'Derek and the Dominos', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	// Two rows for the same release group (the bug), plus one row for a
	// genuinely different release group (must survive untouched).
	if _, err := db.Exec(`
		INSERT INTO albums (id, artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at) VALUES
			(1, 1, 'release-2011', 'rg-layla', 'Layla and Other Assorted Love Songs', '2011', 'Album', ?, ?),
			(2, 1, 'release-1989', 'rg-layla', 'Layla and Other Assorted Love Songs', '1989', 'Album', ?, ?),
			(3, 1, 'release-crossroads', 'rg-crossroads', 'Crossroads', '1988', 'Album', ?, ?)`,
		now, now, now, now, now, now); err != nil {
		t.Fatalf("insert albums: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tracks (id, album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at) VALUES (10, 2, 'track-mbid', 'Bell Bottom Blues', 2, 1, 1000, ?, ?)`, now, now); err != nil {
		t.Fatalf("insert track: %v", err)
	}

	b, err := migrationsFS.ReadFile("migrations/0003_album_release_group_unique.sql")
	if err != nil {
		t.Fatalf("read 0003: %v", err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		t.Fatalf("apply 0003: %v", err)
	}

	var laylaCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM albums WHERE release_group_mbid = 'rg-layla'`).Scan(&laylaCount); err != nil {
		t.Fatal(err)
	}
	if laylaCount != 1 {
		t.Errorf("albums with release_group_mbid=rg-layla = %d, want 1", laylaCount)
	}

	var keptID int64
	if err := db.QueryRow(`SELECT id FROM albums WHERE release_group_mbid = 'rg-layla'`).Scan(&keptID); err != nil {
		t.Fatal(err)
	}
	if keptID != 1 {
		t.Errorf("kept album id = %d, want 1 (lowest id / first recorded)", keptID)
	}

	var crossroadsCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM albums WHERE release_group_mbid = 'rg-crossroads'`).Scan(&crossroadsCount); err != nil {
		t.Fatal(err)
	}
	if crossroadsCount != 1 {
		t.Errorf("albums with release_group_mbid=rg-crossroads = %d, want 1 (a distinct release group must not be touched)", crossroadsCount)
	}

	// The deleted row's track cascaded away with it.
	var trackCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tracks WHERE id = 10`).Scan(&trackCount); err != nil {
		t.Fatal(err)
	}
	if trackCount != 0 {
		t.Errorf("track belonging to the deleted duplicate album still exists, want it cascade-deleted")
	}

	// The unique index now rejects a second row for the same
	// (artist_id, release_group_mbid).
	_, err = db.Exec(`INSERT INTO albums (artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at) VALUES (1, 'release-2004', 'rg-layla', 'Layla and Other Assorted Love Songs', '2004', 'Album', ?, ?)`, now, now)
	if err == nil {
		t.Error("expected a unique constraint violation inserting a second row for the same (artist_id, release_group_mbid)")
	}
}

// TestMigration0004FoldsMonitoredArtistsIntoArtists simulates a live
// database with 0001-0003 already applied and real monitored_artists/
// wanted_albums/downloads data covering both cases 0004 has to handle: an
// artist that was only ever monitored (no owned files — "Boards of
// Canada"), and one already in `artists` from file-matching that later
// also got monitored ("Derek and the Dominos", mirroring the live
// instance's actual id-1 artist). Asserts the merge, the wanted_albums
// FK repoint (ids preserved so downloads keeps pointing at the right
// row), and that monitored_artists/the old column are both gone.
func TestMigration0004FoldsMonitoredArtistsIntoArtists(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}
	db := &DB{DB: sqlDB}

	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_acquisition.sql", "migrations/0003_album_release_group_unique.sql"} {
		b, err := migrationsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	now := time.Now().UTC()
	// Derek and the Dominos: already an `artists` row from file-matching,
	// never actually monitored via the old flow — mirrors the live
	// instance's artist id 1.
	if _, err := db.Exec(`INSERT INTO artists (id, mbid, name, sort_name, created_at, updated_at) VALUES (1, 'derek-mbid', 'Derek and the Dominos', 'Derek and the Dominos', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO albums (id, artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at) VALUES (1, 1, 'release-layla', 'rg-layla', 'Layla and Other Assorted Love Songs', '1970', 'Album', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert album: %v", err)
	}

	// monitored_artists: one that overlaps the existing artist above (by
	// mbid) plus one brand new artist that has no owned files at all.
	if _, err := db.Exec(`INSERT INTO monitored_artists (id, mbid, name, sort_name, added_at, last_synced_at) VALUES
		(1, 'derek-mbid', 'Derek and the Dominos', 'Derek and the Dominos', ?, ?),
		(2, 'boc-mbid', 'Boards of Canada', 'Boards of Canada', ?, NULL)`,
		now, now, now); err != nil {
		t.Fatalf("insert monitored_artists: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO wanted_albums (id, monitored_artist_id, release_group_mbid, title, primary_type, release_date, status, added_at) VALUES
		(1, 2, 'rg-mhrttc', 'Music Has the Right to Children', 'Album', '1998', 'wanted', ?)`, now); err != nil {
		t.Fatalf("insert wanted_albums: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO root_folders (id, path, created_at) VALUES (1, '/music', ?)`, now); err != nil {
		t.Fatalf("insert root_folders: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO downloads (id, wanted_album_id, root_folder_id, protocol, client_id, title, grabbed_at) VALUES
		(1, 1, 1, 'torrent', 'hash', 'Music Has the Right to Children', ?)`, now); err != nil {
		t.Fatalf("insert downloads: %v", err)
	}

	b, err := migrationsFS.ReadFile("migrations/0004_unified_artist.sql")
	if err != nil {
		t.Fatalf("read 0004: %v", err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		t.Fatalf("apply 0004: %v", err)
	}

	// Derek and the Dominos: existing row updated in place, not
	// duplicated, and now flagged monitored.
	var derekCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artists WHERE mbid = 'derek-mbid'`).Scan(&derekCount); err != nil {
		t.Fatal(err)
	}
	if derekCount != 1 {
		t.Errorf("artists with mbid=derek-mbid = %d, want 1 (updated in place, not duplicated)", derekCount)
	}
	var derekMonitored int
	var derekID int64
	if err := db.QueryRow(`SELECT id, is_monitored FROM artists WHERE mbid = 'derek-mbid'`).Scan(&derekID, &derekMonitored); err != nil {
		t.Fatal(err)
	}
	if derekID != 1 {
		t.Errorf("Derek's artist id changed to %d, want 1 (existing row kept in place)", derekID)
	}
	if derekMonitored != 1 {
		t.Error("Derek and the Dominos should be flagged is_monitored=1 after the merge")
	}

	// Boards of Canada: no prior artists row, so 0004 must insert a
	// fresh minimal one.
	var boc struct {
		ID          int64
		IsMonitored int
		Name        string
	}
	if err := db.QueryRow(`SELECT id, is_monitored, name FROM artists WHERE mbid = 'boc-mbid'`).Scan(&boc.ID, &boc.IsMonitored, &boc.Name); err != nil {
		t.Fatalf("Boards of Canada artist row missing after migration: %v", err)
	}
	if boc.IsMonitored != 1 || boc.Name != "Boards of Canada" {
		t.Errorf("Boards of Canada artist = %+v", boc)
	}

	// wanted_albums now points at artists.id (Boards of Canada's new id),
	// and its own row id (1) is unchanged.
	var wantedArtistID int64
	var wantedID int64
	if err := db.QueryRow(`SELECT id, artist_id FROM wanted_albums WHERE release_group_mbid = 'rg-mhrttc'`).Scan(&wantedID, &wantedArtistID); err != nil {
		t.Fatal(err)
	}
	if wantedID != 1 {
		t.Errorf("wanted_albums row id = %d, want 1 (preserved across rebuild)", wantedID)
	}
	if wantedArtistID != boc.ID {
		t.Errorf("wanted_albums.artist_id = %d, want %d (Boards of Canada's artist id)", wantedArtistID, boc.ID)
	}

	// downloads still resolves through to the same wanted_albums row —
	// proof the table rebuild preserved ids rather than reassigning them.
	var downloadWantedID int64
	if err := db.QueryRow(`SELECT wanted_album_id FROM downloads WHERE id = 1`).Scan(&downloadWantedID); err != nil {
		t.Fatal(err)
	}
	if downloadWantedID != 1 {
		t.Errorf("downloads.wanted_album_id = %d, want 1", downloadWantedID)
	}

	// The old column/table are gone, and the new UNIQUE constraint holds.
	if _, err := db.Exec(`SELECT monitored_artist_id FROM wanted_albums LIMIT 0`); err == nil {
		t.Error("wanted_albums.monitored_artist_id should no longer exist")
	}
	if _, err := db.Exec(`SELECT 1 FROM monitored_artists LIMIT 0`); err == nil {
		t.Error("monitored_artists table should have been dropped")
	}
	_, err = db.Exec(`INSERT INTO wanted_albums (artist_id, release_group_mbid, title, added_at) VALUES (?, 'rg-mhrttc', 'dup', ?)`, boc.ID, now)
	if err == nil {
		t.Error("expected a unique constraint violation inserting a second row for the same (artist_id, release_group_mbid)")
	}
}
