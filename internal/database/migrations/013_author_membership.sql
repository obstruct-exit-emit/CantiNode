-- Author-level library membership: an author belongs to the Ebooks and/or
-- Audiobooks library because they were added there (or own files there) —
-- independent of how many of their books are currently visible. This keeps
-- an author on the library page even when every book is unmonitored and
-- unowned (the Missing section lists the bibliography), and makes
-- adding/removing an author in one format library leave the other alone.

ALTER TABLE authors ADD COLUMN in_ebook_library INTEGER NOT NULL DEFAULT 0;
ALTER TABLE authors ADD COLUMN in_audiobook_library INTEGER NOT NULL DEFAULT 0;

-- Backfill from book membership: wherever an author has member books today,
-- the author is a member too.
UPDATE authors SET in_ebook_library = 1
WHERE id IN (SELECT author_id FROM books WHERE media_type = 'book' AND in_ebook_library = 1);

UPDATE authors SET in_audiobook_library = 1
WHERE id IN (SELECT author_id FROM books WHERE media_type = 'book' AND in_audiobook_library = 1);
