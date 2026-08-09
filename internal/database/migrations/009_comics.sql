-- Comics: series become first-class monitored entities whose issues are book
-- rows (media_type comic) linked through series_books (position = issue
-- number). monitor_new controls whether future issues discovered on refresh
-- start monitored. Indexers get a category list for comic searches
-- (7030 = Books/Comics).

ALTER TABLE books ADD COLUMN media_type TEXT NOT NULL DEFAULT 'book';
ALTER TABLE series ADD COLUMN media_type TEXT NOT NULL DEFAULT 'book';
ALTER TABLE series ADD COLUMN monitored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN monitor_new INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN cover_url TEXT NOT NULL DEFAULT '';
ALTER TABLE indexers ADD COLUMN comic_categories TEXT NOT NULL DEFAULT '7030';
