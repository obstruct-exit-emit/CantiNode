-- Audiobook support: files and grabs carry their media type (a book can be
-- owned as ebook and audiobook independently), and indexers get a separate
-- category list for audio searches (Newznab 3030 = Audio/Audiobook).
-- For multi-file audiobooks, book_files.path holds the book's directory.

ALTER TABLE book_files ADD COLUMN media_type TEXT NOT NULL DEFAULT 'ebook';
ALTER TABLE grabs ADD COLUMN media_type TEXT NOT NULL DEFAULT 'ebook';
ALTER TABLE indexers ADD COLUMN audio_categories TEXT NOT NULL DEFAULT '3030';
