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

-- Whichever music root folder was added first (if any) becomes the
-- default — matches importer's own pre-existing "folders[0]" behavior
-- (ListRootFolders orders by path today; the id ordering used here is
-- equivalent for "first ever added" once ids are assigned in insertion
-- order, which they always are), so upgrading through this migration
-- doesn't change where a fresh install's very first configured folder
-- sends new grabs.
UPDATE root_folders SET is_default = 1
WHERE media_type = 'music' AND id = (SELECT MIN(id) FROM root_folders WHERE media_type = 'music');
