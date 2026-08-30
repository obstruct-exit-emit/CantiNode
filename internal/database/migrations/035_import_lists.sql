-- Import lists: periodically resolved external sources (a MusicBrainz
-- Series, a plain pasted/fetched artist list, or a Last.fm user/tag top-
-- artists list) that auto-add and monitor any newly-appearing artist —
-- add-only, matching CantiNode's existing "never auto-delete" posture
-- elsewhere (an artist that later falls off a list stays in the library).
-- Flat typed columns per type, matching the indexers/download_clients
-- convention rather than a JSON config blob.

CREATE TABLE import_lists (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL UNIQUE,
    type            TEXT    NOT NULL CHECK (type IN ('musicbrainz_series', 'list', 'lastfm')),
    -- musicbrainz_series: the series' own MBID.
    series_mbid     TEXT    NOT NULL DEFAULT '',
    -- list: pasted text (one artist name per line) used directly when
    -- source_url is empty, or fetched fresh from source_url on every sync
    -- (same one-line-per-artist shape) when it's set.
    list_text       TEXT    NOT NULL DEFAULT '',
    source_url      TEXT    NOT NULL DEFAULT '',
    -- lastfm: lastfm_kind picks whether lastfm_target names a username
    -- (that user's top artists) or a tag/genre (that tag's top artists).
    lastfm_kind     TEXT    NOT NULL DEFAULT 'user' CHECK (lastfm_kind IN ('user', 'tag')),
    lastfm_target   TEXT    NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    added_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    last_synced_at  TEXT,
    last_sync_error TEXT    NOT NULL DEFAULT ''
);
