-- Plex-style explicit library membership: a prose book belongs to the
-- Ebooks library only when its format is owned or was deliberately added —
-- never inferred implicitly. Carries its own monitored flag.

ALTER TABLE books ADD COLUMN in_ebook_library INTEGER NOT NULL DEFAULT 0;
ALTER TABLE books ADD COLUMN ebook_monitored INTEGER NOT NULL DEFAULT 0;

-- Backfill: every existing prose book was implicitly in the ebook library.
UPDATE books SET in_ebook_library = 1, ebook_monitored = monitored
WHERE media_type = 'book';
