-- release_group_versions previously distinguished a genuinely-fetched row
-- from migration 022's carried-over placeholder (release_mbid/title only,
-- every other column left blank) by guessing from field values
-- (track_count > 0 OR status != '') — a heuristic that could misclassify
-- a real MusicBrainz release whose browse response genuinely has neither
-- field populated (both do happen), causing HasReleaseGroupVersions to
-- treat it as "still needs backfill" forever: unbounded, repeated live
-- MusicBrainz re-fetches for that one release group on every scan and
-- every version-dropdown open, defeating the caching this whole feature
-- exists to provide. An explicit flag replaces the guess.
ALTER TABLE release_group_versions ADD COLUMN fetched INTEGER NOT NULL DEFAULT 1;

-- Retroactively mark whatever still looks like a migration-022 placeholder
-- (the exact heuristic this migration replaces) as NOT fetched — applied
-- exactly once, here, for rows that predate this column. This covers both
-- an existing installation upgrading through 022 then 023, and a fresh
-- install where 022 inserted its placeholder rows moments before 023 runs
-- in the same migration batch.
UPDATE release_group_versions SET fetched = 0 WHERE track_count = 0 AND status = '';
