-- Manga colorized/monochrome variants. Manga stays ONE library (unlike the
-- ebook/audiobook split): a variant is a sub-dimension of manga files, not a
-- separate media type. Each manga root folder is tagged with the variant it
-- holds ('color' or 'mono'), and every scanned/imported file inherits its
-- root's variant — so a single volume can own the colorized copy, the
-- monochrome copy, or both, all sharing one series/volume metadata row.
--
-- variant is empty for every non-manga root and file (they have no variants).

ALTER TABLE root_folders ADD COLUMN variant TEXT NOT NULL DEFAULT '';
ALTER TABLE book_files ADD COLUMN variant TEXT NOT NULL DEFAULT '';

-- Existing manga roots predate variants; monochrome is the standard form of
-- manga, so default them there. Colorized roots are the deliberate exception
-- the user adds explicitly.
UPDATE root_folders SET variant = 'mono' WHERE media_type = 'manga' AND variant = '';

-- Backfill each file's variant from the root it lives under (a no-op for
-- non-manga roots, which stay '').
UPDATE book_files
   SET variant = COALESCE((SELECT variant FROM root_folders WHERE root_folders.id = book_files.root_folder_id), '')
 WHERE variant = '';
