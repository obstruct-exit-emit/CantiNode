-- tracks.mbid was globally UNIQUE, so a MusicBrainz recording shared by
-- two different releases (e.g. a single also included on its parent
-- album) could only ever have one track row -- any file matched to that
-- recording, however it was tagged, always resolved to whichever
-- album's track already claimed it. Found live: a Blind Melon "Change"
-- single track, correctly tagged with the single's own release, still
-- resolved to the self-titled album's own "Change" track, because that
-- recording's track row was created first. Organize then correctly
-- refused to place the single's own file at the album's already-occupied
-- destination -- surfacing as a confusing "destination already exists"
-- error with no obvious connection to the real cause.
--
-- Uniqueness moves to (album_id, mbid) -- the same album-scoped pattern
-- already used for albums.release_group_mbid (idx_albums_artist_release_group)
-- -- so the identical recording can have one track row per album it
-- genuinely belongs to, while two files matched to the same recording
-- WITHIN the same album still correctly collide (a real duplicate rip of
-- the same album track, not two different releases). SQLite can't alter
-- a column-level UNIQUE constraint in place, so the table is rebuilt.

CREATE TABLE tracks_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id     INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    mbid         TEXT,
    title        TEXT NOT NULL,
    track_number INTEGER NOT NULL DEFAULT 0,
    disc_number  INTEGER NOT NULL DEFAULT 1,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at   TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    artist_credit TEXT NOT NULL DEFAULT ''
);

INSERT INTO tracks_new (id, album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at, artist_credit)
    SELECT id, album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at, artist_credit
    FROM tracks;

DROP TABLE tracks;
ALTER TABLE tracks_new RENAME TO tracks;

CREATE INDEX idx_tracks_album_id ON tracks (album_id);
CREATE UNIQUE INDEX idx_tracks_album_mbid ON tracks (album_id, mbid) WHERE mbid != '';
