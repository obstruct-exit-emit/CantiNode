-- Playlists: user-curated ordered lists of tracks, independent of any
-- album/artist. Items reference tracks (the musical work), not track_files
-- (the physical file) — a playlist entry survives reorganization/rematch,
-- and its actual file is resolved at use time (export) from whichever
-- track_file currently backs that track, if any.

CREATE TABLE playlists (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

-- position is a plain sort key, not uniquely constrained: reordering
-- rewrites every row's position in one transaction rather than juggling
-- gaps, so a moment mid-reorder could (in principle) share a value —
-- harmless, since ORDER BY only needs a consistent relative order, not
-- uniqueness.
CREATE TABLE playlist_items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    track_id    INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    added_at    TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_playlist_items_playlist_id ON playlist_items (playlist_id, position);
CREATE INDEX idx_playlist_items_track_id ON playlist_items (track_id);
