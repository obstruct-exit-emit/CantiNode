-- Links a CantiNode playlist to its own Plex playlist counterpart, for
-- two-way sync (internal/plexplaylistsync). NULL plex_rating_key means
-- "not yet linked" -- every existing playlist starts unlinked; a sync
-- pass creates the link the first time it either pushes this playlist to
-- Plex or pulls a new one from it.

ALTER TABLE playlists ADD COLUMN plex_rating_key TEXT;

-- plex_synced_at records this playlist's own updated_at value (not
-- wall-clock time) as of the last successful sync -- comparing the
-- CURRENT updated_at against this tells the sync loop whether
-- CantiNode's own side has changed since then, independent of whatever
-- Plex's own updatedAt says.
ALTER TABLE playlists ADD COLUMN plex_synced_at TIMESTAMP;

-- plex_updated_at mirrors Plex's own last-known updatedAt (a Unix
-- timestamp Plex itself reports) as of the last successful sync --
-- comparing Plex's CURRENT updatedAt against this tells the sync loop
-- whether Plex's own side has changed since then.
ALTER TABLE playlists ADD COLUMN plex_updated_at INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX idx_playlists_plex_rating_key ON playlists (plex_rating_key) WHERE plex_rating_key IS NOT NULL;

-- Remembers a Plex ratingKey whose own CantiNode-side playlist was just
-- deleted while still linked to it -- so the next sync pass never
-- "resurrects" it as a brand-new CantiNode playlist just because Plex's
-- own copy still exists (or a propagate-mode delete to Plex itself
-- transiently failed). Not cleaned up automatically -- one row per
-- playlist ever deleted while linked is not a real storage concern at
-- any realistic scale.
CREATE TABLE plex_playlist_tombstones (
    plex_rating_key TEXT PRIMARY KEY,
    deleted_at      TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);
