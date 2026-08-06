-- Folds monitored_artists into artists, so every artist CantiNode knows
-- about — whether from owning files, from being explicitly monitored, or
-- both — is one row instead of two disconnected tables the UI had to
-- stitch back together itself. See internal/database/artists.go and
-- internal/acquisition/monitor.go for the Go-side behavior this enables:
-- one unified per-artist page instead of separate Library/Wanted tabs.
ALTER TABLE artists ADD COLUMN is_monitored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE artists ADD COLUMN last_synced_at TIMESTAMP;
ALTER TABLE artists ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN image_url TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN metadata_fetched_at TIMESTAMP;

-- artist_release_groups is the cached discography (every MusicBrainz
-- release group for an artist, any primary/secondary type) backing the
-- unified page's "Missing" section. Populated on first monitor/refresh —
-- see acquisition.Service.MonitorArtist/RefreshArtistMetadata — and never
-- fetched just from browsing.
CREATE TABLE artist_release_groups (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_id          INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    release_group_mbid TEXT NOT NULL,
    title              TEXT NOT NULL,
    primary_type       TEXT NOT NULL DEFAULT '',
    secondary_types    TEXT NOT NULL DEFAULT '',
    first_release_date TEXT NOT NULL DEFAULT '',
    UNIQUE(artist_id, release_group_mbid)
);

CREATE INDEX idx_artist_release_groups_artist_id ON artist_release_groups(artist_id);

-- Fold monitored_artists into artists: an artist already owning files
-- (matched by mbid) just gets flagged is_monitored/last_synced_at; an
-- artist that was only ever monitored (no owned files yet) gets a fresh
-- minimal artists row.
UPDATE artists
SET is_monitored   = 1,
    last_synced_at = (SELECT m.last_synced_at FROM monitored_artists m WHERE m.mbid = artists.mbid)
WHERE mbid IN (SELECT mbid FROM monitored_artists);

INSERT INTO artists (mbid, name, sort_name, is_monitored, last_synced_at, created_at, updated_at)
SELECT m.mbid, m.name, m.sort_name, 1, m.last_synced_at, m.added_at, m.added_at
FROM monitored_artists m
WHERE m.mbid NOT IN (SELECT mbid FROM artists WHERE mbid IS NOT NULL);

-- Repoint wanted_albums at artists instead of monitored_artists. Backfill
-- through the mbid mapping established above while monitored_artists
-- still exists, then rebuild the table: SQLite can't drop a column that's
-- part of a FK/UNIQUE constraint (monitored_artist_id is both here), so
-- the old table has to be recreated wholesale rather than altered in
-- place — same 12-step create/copy/drop/rename procedure as any SQLite
-- schema change that touches a constrained column. Row ids are carried
-- over unchanged so downloads.wanted_album_id (FK'd to this table) keeps
-- pointing at the right rows with no further work.
ALTER TABLE wanted_albums ADD COLUMN artist_id INTEGER;

UPDATE wanted_albums
SET artist_id = (
    SELECT a.id FROM artists a
    JOIN monitored_artists m ON m.mbid = a.mbid
    WHERE m.id = wanted_albums.monitored_artist_id
);

-- Dropping a table that's the parent side of a foreign key makes SQLite
-- run an implicit "DELETE FROM" on it first, to fire any ON DELETE
-- actions of tables that reference it
-- (https://sqlite.org/foreignkeys.html) — so dropping the old
-- wanted_albums below would cascade-delete every downloads row via its
-- own ON DELETE CASCADE, despite nothing here actually asking for that.
-- The usual fix is PRAGMA foreign_keys=OFF around the rebuild, but that
-- pragma is a documented no-op mid-transaction, and every migration here
-- runs inside one (see database.go's migrate). So instead: strip
-- downloads' foreign key down to a plain column first (safe — nothing
-- else references downloads, so downloads itself can be dropped/rebuilt
-- freely), do the wanted_albums rebuild with no live FK pointing at the
-- old table, then rebuild downloads once more to restore its FK against
-- the new wanted_albums table.
CREATE TABLE downloads_tmp (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    wanted_album_id INTEGER NOT NULL,
    root_folder_id  INTEGER NOT NULL REFERENCES root_folders(id),
    protocol        TEXT NOT NULL,
    client_id       TEXT NOT NULL,
    title           TEXT NOT NULL,
    indexer         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'downloading',
    error_message   TEXT NOT NULL DEFAULT '',
    grabbed_at      TIMESTAMP NOT NULL,
    completed_at    TIMESTAMP,
    imported_at     TIMESTAMP
);
INSERT INTO downloads_tmp SELECT * FROM downloads;
DROP TABLE downloads;
ALTER TABLE downloads_tmp RENAME TO downloads;

CREATE TABLE wanted_albums_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_id          INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    release_group_mbid TEXT NOT NULL,
    title              TEXT NOT NULL,
    primary_type       TEXT NOT NULL DEFAULT '',
    release_date       TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'wanted',
    added_at           TIMESTAMP NOT NULL,
    UNIQUE(artist_id, release_group_mbid)
);

INSERT INTO wanted_albums_new (id, artist_id, release_group_mbid, title, primary_type, release_date, status, added_at)
SELECT id, artist_id, release_group_mbid, title, primary_type, release_date, status, added_at FROM wanted_albums;

DROP TABLE wanted_albums;
ALTER TABLE wanted_albums_new RENAME TO wanted_albums;

CREATE INDEX idx_wanted_albums_artist_id ON wanted_albums(artist_id);
CREATE INDEX idx_wanted_albums_status ON wanted_albums(status);

-- Restore downloads' foreign key, now against the rebuilt wanted_albums.
CREATE TABLE downloads_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    wanted_album_id INTEGER NOT NULL REFERENCES wanted_albums(id) ON DELETE CASCADE,
    root_folder_id  INTEGER NOT NULL REFERENCES root_folders(id),
    protocol        TEXT NOT NULL,
    client_id       TEXT NOT NULL,
    title           TEXT NOT NULL,
    indexer         TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'downloading',
    error_message   TEXT NOT NULL DEFAULT '',
    grabbed_at      TIMESTAMP NOT NULL,
    completed_at    TIMESTAMP,
    imported_at     TIMESTAMP
);
INSERT INTO downloads_new SELECT * FROM downloads;
DROP TABLE downloads;
ALTER TABLE downloads_new RENAME TO downloads;

CREATE INDEX idx_downloads_wanted_album_id ON downloads(wanted_album_id);
CREATE INDEX idx_downloads_status ON downloads(status);

DROP TABLE monitored_artists;
