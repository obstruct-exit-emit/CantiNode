-- Music domain: artists/albums/tracks/track_files, matched against
-- MusicBrainz — ported from CantiNode's own original (pre-LibriNode-fork)
-- schema. Kept as dedicated tables rather than folded into books/authors:
-- track-level matching (disc/track position, per-file MusicBrainz
-- recording IDs, embedded-tag confidence) doesn't fit the prose
-- book/edition shape, and an artist's identity is its MusicBrainz MBID,
-- not a books-style (source, foreign_id) pair.
--
-- track_files.root_folder_id points at the same root_folders table every
-- other library uses (a music root is just another row there, media_type
-- 'music' once the Go side switches over) rather than a second
-- music-specific root folder table.
--
-- An artist is unified the same way LibriNode's own authors are: one row
-- whether CantiNode knows about it from owning a matched track file,
-- from being explicitly monitored for acquisition, or both — is_monitored
-- plus artist_release_groups (the cached discography backing the artist
-- page's Missing section) rather than a separate "monitored artists"
-- table.

CREATE TABLE artists (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    mbid                TEXT UNIQUE,
    name                TEXT NOT NULL,
    sort_name           TEXT NOT NULL DEFAULT '',
    is_monitored        INTEGER NOT NULL DEFAULT 0,
    last_synced_at      TIMESTAMP,
    bio                 TEXT NOT NULL DEFAULT '',
    image_url           TEXT NOT NULL DEFAULT '',
    metadata_fetched_at TIMESTAMP,
    created_at          TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at          TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

-- Cached discography (every MusicBrainz release group for an artist, any
-- primary/secondary type) backing the artist page's Missing section.
-- Populated on first monitor/refresh, never fetched just from browsing.
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

CREATE INDEX idx_artist_release_groups_artist_id ON artist_release_groups (artist_id);

-- Album identity is the MusicBrainz release GROUP (the canonical album),
-- not a specific release (one pressing/edition): a recording independently
-- resolves to whichever of its own releases musicbrainz.Recording.BestRelease
-- picks, so two tracks of the very same physical album can carry two
-- different release mbids — GetOrCreateAlbum dedupes on release_group_mbid
-- so they still collapse into one album row.
CREATE TABLE albums (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_id          INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    mbid               TEXT UNIQUE,
    release_group_mbid TEXT,
    title              TEXT NOT NULL,
    release_date       TEXT NOT NULL DEFAULT '',
    primary_type       TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at         TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_albums_artist_id ON albums (artist_id);
CREATE UNIQUE INDEX idx_albums_artist_release_group
    ON albums (artist_id, release_group_mbid)
    WHERE release_group_mbid != '';

CREATE TABLE tracks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id     INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    mbid         TEXT UNIQUE,
    title        TEXT NOT NULL,
    track_number INTEGER NOT NULL DEFAULT 0,
    disc_number  INTEGER NOT NULL DEFAULT 1,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at   TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_tracks_album_id ON tracks (album_id);

-- match_status: 'unmatched' (scanned, no confident match yet), 'matched'
-- (auto-matched by embedded MBID tag or a fuzzy search above threshold),
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
    scanned_at       TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    organized_at     TIMESTAMP
);

CREATE INDEX idx_track_files_root_folder_id ON track_files (root_folder_id);
CREATE INDEX idx_track_files_track_id ON track_files (track_id);
CREATE INDEX idx_track_files_match_status ON track_files (match_status);

-- Wanted albums: a monitored artist's release groups CantiNode should try
-- to acquire. Seeded from MusicBrainz when an artist is monitored and
-- updated by the grab/import flow (internal/autosearch, internal/importer)
-- as a specific one progresses.
--
-- status: 'wanted' (not yet grabbed) -> 'downloading' (a grabs row exists
-- and isn't finished) -> 'downloaded' (imported into the library) ->
-- 'ignored' (user doesn't want this one). Grabbing/downloading itself
-- rides LibriNode's existing indexer/download-client/grabs pipeline
-- (internal/indexer, internal/download) rather than a second one.
CREATE TABLE wanted_albums (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_id          INTEGER NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    release_group_mbid TEXT NOT NULL,
    title              TEXT NOT NULL,
    primary_type       TEXT NOT NULL DEFAULT '',
    release_date       TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'wanted',
    added_at           TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    UNIQUE(artist_id, release_group_mbid)
);

CREATE INDEX idx_wanted_albums_artist_id ON wanted_albums (artist_id);
CREATE INDEX idx_wanted_albums_status ON wanted_albums (status);

-- Release-group tracklist cache: the Missing/Wanted sections' "preview the
-- tracks" action needs a full tracklist for an album CantiNode doesn't own
-- yet, which isn't part of the artist-level metadata cache (that only
-- stores each release group's title/type/date — a real tracklist means
-- picking one specific release out of the group and fetching its full
-- medium/track breakdown, two more MusicBrainz requests). Fetched lazily,
-- once, the first time a given release group's tracks are previewed by
-- anyone; a tracklist essentially never changes once released, so there's
-- no freshness expiry — same "cache once, never poll" policy as the rest
-- of CantiNode's MusicBrainz data.
CREATE TABLE release_group_tracklist_cache (
    release_group_mbid TEXT PRIMARY KEY,
    release_mbid        TEXT NOT NULL,
    release_title       TEXT NOT NULL,
    tracks_json         TEXT NOT NULL,
    fetched_at          TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);
