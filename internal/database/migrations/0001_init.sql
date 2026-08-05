CREATE TABLE root_folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE artists (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    mbid       TEXT UNIQUE,
    name       TEXT NOT NULL,
    sort_name  TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE albums (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_id          INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    mbid               TEXT UNIQUE,
    release_group_mbid TEXT,
    title              TEXT NOT NULL,
    release_date       TEXT NOT NULL DEFAULT '',
    primary_type       TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);

CREATE INDEX idx_albums_artist_id ON albums(artist_id);

CREATE TABLE tracks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id      INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    mbid          TEXT UNIQUE,
    title         TEXT NOT NULL,
    track_number  INTEGER NOT NULL DEFAULT 0,
    disc_number   INTEGER NOT NULL DEFAULT 1,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

CREATE INDEX idx_tracks_album_id ON tracks(album_id);

-- match_status: 'unmatched' (scanned, no confident match yet),
-- 'matched' (auto-matched by MBID tag or fuzzy search above threshold),
-- 'manual' (linked to a track by a human through the review UI).
CREATE TABLE track_files (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    root_folder_id   INTEGER NOT NULL REFERENCES root_folders(id) ON DELETE CASCADE,
    track_id         INTEGER REFERENCES tracks(id) ON DELETE SET NULL,
    path             TEXT NOT NULL UNIQUE,
    size_bytes       INTEGER NOT NULL DEFAULT 0,
    format           TEXT NOT NULL DEFAULT '',
    bitrate_kbps     INTEGER NOT NULL DEFAULT 0,
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    tags_json        TEXT NOT NULL DEFAULT '{}',
    match_status     TEXT NOT NULL DEFAULT 'unmatched',
    match_confidence REAL NOT NULL DEFAULT 0,
    scanned_at       TIMESTAMP NOT NULL,
    organized_at     TIMESTAMP
);

CREATE INDEX idx_track_files_root_folder_id ON track_files(root_folder_id);
CREATE INDEX idx_track_files_track_id ON track_files(track_id);
CREATE INDEX idx_track_files_match_status ON track_files(match_status);
