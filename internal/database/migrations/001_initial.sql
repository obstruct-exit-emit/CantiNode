-- LibriNode initial schema.
-- Media types are fixed strings: 'ebook', 'audiobook', 'manga', 'comic'.

CREATE TABLE root_folders (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type  TEXT    NOT NULL CHECK (media_type IN ('ebook', 'audiobook', 'manga', 'comic')),
    path        TEXT    NOT NULL UNIQUE,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_root_folders_media_type ON root_folders (media_type);
