-- Ebook and comic support removed entirely — music is now the only media
-- type. Drops the whole prose/comic domain (authors, books, editions,
-- series, series_books, book_files) and narrows every shared table's
-- media_type CHECK down to 'music' (SQLite can't ALTER a CHECK constraint,
-- so root_folders/quality_profiles/indexers are rebuilt, same pattern as
-- 016_native_indexers.sql). Foreign keys are off for the whole migration
-- run (see database.go's migrate()), so dropping authors/books/series
-- ahead of the tables that used to reference them is safe.

DROP TABLE book_files;
DROP TABLE series_books;
DROP TABLE editions;
DROP TABLE books;
DROP TABLE series;
DROP TABLE authors;

-- root_folders: CHECK ('ebook','audiobook','comic','music') -> ('music').
CREATE TABLE root_folders_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type  TEXT    NOT NULL CHECK (media_type IN ('music')),
    path        TEXT    NOT NULL UNIQUE,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO root_folders_new (id, media_type, path, created_at)
    SELECT id, media_type, path, created_at FROM root_folders WHERE media_type = 'music';
DROP TABLE root_folders;
ALTER TABLE root_folders_new RENAME TO root_folders;
CREATE INDEX idx_root_folders_media_type ON root_folders (media_type);

-- quality_profiles: CHECK narrowed to 'music'; the seeded 'Standard Ebook'
-- default (media_type 'ebook') is dropped by the WHERE filter below, and a
-- 'Standard Music' default takes its place so a fresh default still exists
-- for release scoring (internal/release.PreferencesFor).
CREATE TABLE quality_profiles_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    media_type   TEXT    NOT NULL DEFAULT 'music'
                 CHECK (media_type IN ('music')),
    formats      TEXT    NOT NULL,
    language     TEXT    NOT NULL DEFAULT 'english',
    retail_bonus INTEGER NOT NULL DEFAULT 25,
    min_size     INTEGER NOT NULL DEFAULT 20480,
    max_size     INTEGER NOT NULL DEFAULT 524288000,
    upgrades_allowed INTEGER NOT NULL DEFAULT 0,
    cutoff       TEXT    NOT NULL DEFAULT '',
    is_default   INTEGER NOT NULL DEFAULT 0,
    added_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO quality_profiles_new (id, name, media_type, formats, language, retail_bonus, min_size, max_size, upgrades_allowed, cutoff, is_default, added_at)
    SELECT id, name, media_type, formats, language, retail_bonus, min_size, max_size, upgrades_allowed, cutoff, is_default, added_at
    FROM quality_profiles WHERE media_type = 'music';
DROP TABLE quality_profiles;
ALTER TABLE quality_profiles_new RENAME TO quality_profiles;

INSERT INTO quality_profiles (name, media_type, formats, min_size, max_size, is_default)
SELECT 'Standard Music', 'music', 'flac,mp3,m4a,opus,wav', 1048576, 4294967296, 1
WHERE NOT EXISTS (SELECT 1 FROM quality_profiles WHERE media_type = 'music' AND is_default = 1);

-- indexers: drop the ebook-only `categories` and comic-only
-- `comic_categories` columns, keeping just `audio_categories`.
CREATE TABLE indexers_new (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT    NOT NULL UNIQUE,
    type                TEXT    NOT NULL,
    base_url            TEXT    NOT NULL DEFAULT '',
    api_key             TEXT    NOT NULL DEFAULT '',
    audio_categories    TEXT    NOT NULL DEFAULT '3010,3040',
    enabled             INTEGER NOT NULL DEFAULT 1,
    priority            INTEGER NOT NULL DEFAULT 25,
    added_at            TEXT    NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO indexers_new (id, name, type, base_url, api_key, audio_categories, enabled, priority, added_at)
    SELECT id, name, type, base_url, api_key, audio_categories, enabled, priority, added_at
    FROM indexers;
DROP TABLE indexers;
ALTER TABLE indexers_new RENAME TO indexers;

-- grabs: book_id's REFERENCES books(id) is dropped along with the table
-- (SQLite has no ALTER…DROP CONSTRAINT); the column itself stays as a plain
-- nullable identifier — music grabs always insert it NULL/0 (untracked),
-- same as before.
CREATE TABLE grabs_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id          INTEGER,
    client_config_id INTEGER REFERENCES download_clients(id) ON DELETE SET NULL,
    client_item_id   TEXT    NOT NULL DEFAULT '',
    title            TEXT    NOT NULL,
    guid             TEXT    NOT NULL DEFAULT '',
    protocol         TEXT    NOT NULL,
    media_type       TEXT    NOT NULL DEFAULT 'music',
    status           TEXT    NOT NULL DEFAULT 'grabbed'
                     CHECK (status IN ('grabbed', 'imported', 'failed')),
    message          TEXT    NOT NULL DEFAULT '',
    grabbed_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);
INSERT INTO grabs_new (id, book_id, client_config_id, client_item_id, title, guid, protocol, media_type, status, message, grabbed_at, completed_at)
    SELECT id, book_id, client_config_id, client_item_id, title, guid, protocol, media_type, status, message, grabbed_at, completed_at
    FROM grabs;
DROP TABLE grabs;
ALTER TABLE grabs_new RENAME TO grabs;
CREATE INDEX idx_grabs_status ON grabs (status);
