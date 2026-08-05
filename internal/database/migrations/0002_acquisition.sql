-- Monitored artists: an artist the user wants CantiNode to keep an eye
-- on. Deliberately separate from `artists` (internal/database/artists.go)
-- — that table only ever holds an artist once something scanned from
-- disk has actually matched to them; this one exists purely from the
-- user's own "watch this artist" intent, independent of anything being
-- on disk yet.
CREATE TABLE monitored_artists (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    mbid           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    sort_name      TEXT NOT NULL DEFAULT '',
    added_at       TIMESTAMP NOT NULL,
    last_synced_at TIMESTAMP
);

-- Wanted albums: a monitored artist's release groups CantiNode should
-- try to acquire. Seeded from MusicBrainz when an artist is monitored
-- (primary_type = 'Album' release groups only by default — see
-- internal/acquisition) and updated by the grab/import flow as a
-- specific one progresses.
--
-- status: 'wanted' (not yet grabbed) -> 'downloading' (a download row
-- exists and isn't finished) -> 'downloaded' (imported into the
-- library) -> 'ignored' (user doesn't want this one). Deliberately not
-- 'downloading' -> 'wanted' automatically on a failed download — see
-- internal/acquisition's own retry handling.
CREATE TABLE wanted_albums (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    monitored_artist_id INTEGER NOT NULL REFERENCES monitored_artists(id) ON DELETE CASCADE,
    release_group_mbid  TEXT NOT NULL,
    title               TEXT NOT NULL,
    primary_type        TEXT NOT NULL DEFAULT '',
    release_date        TEXT NOT NULL DEFAULT '',
    status              TEXT NOT NULL DEFAULT 'wanted',
    added_at            TIMESTAMP NOT NULL,
    UNIQUE(monitored_artist_id, release_group_mbid)
);

CREATE INDEX idx_wanted_albums_monitored_artist_id ON wanted_albums(monitored_artist_id);
CREATE INDEX idx_wanted_albums_status ON wanted_albums(status);

-- Downloads: one grabbed release, from the moment it's sent to AcerviNode
-- until its files are imported into the library (or it errors out).
--
-- client_id is the identifier used to poll AcerviNode for this specific
-- download's status: a torrent's infohash (protocol='torrent', polled via
-- the qBittorrent shim's /api/v2/torrents/info?hashes=) or a usenet
-- nzo_id (protocol='usenet', polled via the SABnzbd shim's mode=queue/
-- mode=history) — see internal/acervinode.
--
-- status: 'downloading' -> 'completed' (AcerviNode has the files on its
-- own local disk) -> 'imported' (copied into root_folder_id and handed
-- to internal/scanner), or 'error' at any point.
CREATE TABLE downloads (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    wanted_album_id INTEGER NOT NULL REFERENCES wanted_albums(id) ON DELETE CASCADE,
    root_folder_id INTEGER NOT NULL REFERENCES root_folders(id),
    protocol       TEXT NOT NULL,
    client_id      TEXT NOT NULL,
    title          TEXT NOT NULL,
    indexer        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'downloading',
    error_message  TEXT NOT NULL DEFAULT '',
    grabbed_at     TIMESTAMP NOT NULL,
    completed_at   TIMESTAMP,
    imported_at    TIMESTAMP
);

CREATE INDEX idx_downloads_wanted_album_id ON downloads(wanted_album_id);
CREATE INDEX idx_downloads_status ON downloads(status);
