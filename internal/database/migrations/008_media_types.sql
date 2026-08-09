-- Files and grabs carry their media type (a book can be owned as ebook,
-- or as a comic issue, independently), and indexers get a separate
-- category list for audio/music searches (Newznab 3010 = Audio/MP3,
-- 3040 = Audio/Lossless).

ALTER TABLE book_files ADD COLUMN media_type TEXT NOT NULL DEFAULT 'ebook';
ALTER TABLE grabs ADD COLUMN media_type TEXT NOT NULL DEFAULT 'ebook';
ALTER TABLE indexers ADD COLUMN audio_categories TEXT NOT NULL DEFAULT '3010,3040';
