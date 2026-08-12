-- Release-version selection + fuller metadata caching.
--
-- Previously a release group collapsed to exactly one "representative"
-- release (see internal/api's pickRepresentativeRelease), with its
-- tracklist the only thing ever cached (release_group_tracklist_cache,
-- keyed by release_group_mbid). Now the matching UI lets a user pick which
-- specific version/edition of an album a folder of files actually is, so
-- every known version needs its own cached metadata and its own tracklist
-- — release_group_versions replaces the single-release assumption,
-- release_tracklist_cache replaces release_group_tracklist_cache (now
-- keyed per release, not per release group).

CREATE TABLE release_group_versions (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    release_group_mbid TEXT NOT NULL,
    release_mbid       TEXT NOT NULL,
    title              TEXT NOT NULL DEFAULT '',
    release_date       TEXT NOT NULL DEFAULT '',
    country            TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT '',
    disambiguation     TEXT NOT NULL DEFAULT '',
    track_count        INTEGER NOT NULL DEFAULT 0,
    media_summary      TEXT NOT NULL DEFAULT '',
    is_representative  INTEGER NOT NULL DEFAULT 0,
    fetched_at         TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    UNIQUE(release_group_mbid, release_mbid)
);

CREATE INDEX idx_release_group_versions_rg ON release_group_versions (release_group_mbid);

-- Per-release (not per-release-group) full tracklist — a release group
-- with 3 cached versions now has 3 tracklist rows, one per release_mbid,
-- instead of the old scheme's single collapsed tracklist.
CREATE TABLE release_tracklist_cache (
    release_mbid       TEXT PRIMARY KEY,
    release_group_mbid TEXT NOT NULL,
    tracks_json        TEXT NOT NULL,
    fetched_at         TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_release_tracklist_cache_rg ON release_tracklist_cache (release_group_mbid);

-- Carry over whatever was already warm under the old single-release
-- scheme, so an existing library doesn't cold-start its whole cache —
-- each old row becomes one version (flagged representative, the same
-- release pickRepresentativeRelease would have picked) plus its tracklist.
-- Metadata this migration can't backfill from the old row alone (country,
-- status, track count, disambiguation, media summary) stays blank until
-- the next artist refresh/backfill sweep re-fetches it.
INSERT INTO release_group_versions (release_group_mbid, release_mbid, title, is_representative, fetched_at)
    SELECT release_group_mbid, release_mbid, release_title, 1, fetched_at FROM release_group_tracklist_cache;
INSERT INTO release_tracklist_cache (release_mbid, release_group_mbid, tracks_json, fetched_at)
    SELECT release_mbid, release_group_mbid, tracks_json, fetched_at FROM release_group_tracklist_cache;

DROP TABLE release_group_tracklist_cache;

-- Artist-level genres/tags/rating (MusicBrainz inc=genres+tags+ratings) —
-- cached alongside bio/image even though nothing displays them yet, so a
-- future feature never needs a fresh MusicBrainz round trip for data
-- already fetched once. genres/tags stored comma-joined, same convention
-- as artist_release_groups.secondary_types.
ALTER TABLE artists ADD COLUMN genres TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN tags TEXT NOT NULL DEFAULT '';
ALTER TABLE artists ADD COLUMN rating_value REAL NOT NULL DEFAULT 0;
ALTER TABLE artists ADD COLUMN rating_votes INTEGER NOT NULL DEFAULT 0;
