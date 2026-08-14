-- Multiple named root folders, all serving the same music library, with
-- one marked as the default destination for a new automatic grab that
-- doesn't already have an artist-specific folder to join (see
-- internal/importer's targetRootFolder). Existing folders are named after
-- their own path (a reasonable starting point — SQLite has no portable
-- basename() to compute something shorter here) and freely renameable via
-- the API afterward.
ALTER TABLE root_folders ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE root_folders ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;

UPDATE root_folders SET name = path WHERE name = '';

-- Whichever music root folder was added first (lowest id, i.e. insertion
-- order) becomes the default. Note this is NOT necessarily the same
-- folder the old hardcoded "folders[0]" import destination picked pre-
-- migration — that read from ListRootFolders, which ordered by path
-- (alphabetical), not insertion order — so an instance upgrading with two
-- or more pre-existing root folders could see new grabs start landing in
-- a different one than before. A one-time, unannounced redirect for that
-- specific (rare) case, not a correctness bug: every root folder still
-- serves the same library either way, and the new default is always
-- visible and changeable in Settings from the moment this migration runs.
UPDATE root_folders SET is_default = 1
WHERE media_type = 'music' AND id = (SELECT MIN(id) FROM root_folders WHERE media_type = 'music');
