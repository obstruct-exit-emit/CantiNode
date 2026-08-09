-- Plex-style explicit per-format library membership: a prose book belongs to
-- the Ebooks and/or Audiobooks library only when its format is owned or was
-- deliberately added — never inferred from the other format. Each membership
-- carries its own monitored flag; this replaces monitored audiobook editions
-- as the audiobook wanted signal.

ALTER TABLE books ADD COLUMN in_ebook_library INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN ebook_monitored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN in_audiobook_library INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN audiobook_monitored INTEGER NOT NULL DEFAULT 0;

-- Backfill: every existing prose book was implicitly in the ebook library.
UPDATE books SET in_ebook_library = 1, ebook_monitored = monitored
WHERE media_type = 'book';

-- Audiobook membership carried over from the old opt-ins: an owned audiobook
-- file, or a monitored audiobook edition.
UPDATE books SET in_audiobook_library = 1
WHERE media_type = 'book' AND (
    id IN (SELECT book_id FROM book_files WHERE media_type = 'audiobook' AND book_id IS NOT NULL)
    OR id IN (SELECT book_id FROM editions WHERE format = 'audiobook' AND monitored = 1)
);

UPDATE books SET audiobook_monitored = 1
WHERE in_audiobook_library = 1 AND monitored = 1
  AND id IN (SELECT book_id FROM editions WHERE format = 'audiobook' AND monitored = 1);
