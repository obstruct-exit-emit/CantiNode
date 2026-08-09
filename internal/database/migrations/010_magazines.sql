-- Magazines: a fifth media type. Magazines are provider-less series added
-- by name; issues are recognized from release/file names by date or issue
-- number and created on grab or scan. root_folders and quality_profiles
-- carry CHECK constraints listing the media types, and SQLite can't alter
-- constraints — so both tables are rebuilt (foreign keys are off during
-- migrations).

CREATE TABLE root_folders_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type  TEXT    NOT NULL CHECK (media_type IN ('ebook', 'audiobook', 'manga', 'comic', 'magazine')),
    path        TEXT    NOT NULL UNIQUE,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO root_folders_new (id, media_type, path, created_at)
    SELECT id, media_type, path, created_at FROM root_folders;
DROP TABLE root_folders;
ALTER TABLE root_folders_new RENAME TO root_folders;
CREATE INDEX idx_root_folders_media_type ON root_folders (media_type);

CREATE TABLE quality_profiles_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    media_type   TEXT    NOT NULL DEFAULT 'ebook'
                 CHECK (media_type IN ('ebook', 'audiobook', 'manga', 'comic', 'magazine')),
    formats      TEXT    NOT NULL,
    language     TEXT    NOT NULL DEFAULT 'english',
    retail_bonus INTEGER NOT NULL DEFAULT 25,
    min_size     INTEGER NOT NULL DEFAULT 20480,
    max_size     INTEGER NOT NULL DEFAULT 524288000,
    is_default   INTEGER NOT NULL DEFAULT 0,
    added_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO quality_profiles_new
    SELECT id, name, media_type, formats, language, retail_bonus, min_size, max_size, is_default, added_at
    FROM quality_profiles;
DROP TABLE quality_profiles;
ALTER TABLE quality_profiles_new RENAME TO quality_profiles;

ALTER TABLE indexers ADD COLUMN magazine_categories TEXT NOT NULL DEFAULT '7010';
