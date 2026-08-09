-- Indexers: Newznab (usenet) and Torznab (torrent) endpoints. Categories are
-- comma-separated Newznab category ids (7000 Books, 7020 Books/Ebook; 3030
-- Audio/Audiobook arrives with Phase 3). Priority follows *arr conventions:
-- 1-50, lower wins ties when grabbing.

CREATE TABLE indexers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    type       TEXT    NOT NULL CHECK (type IN ('newznab', 'torznab')),
    base_url   TEXT    NOT NULL,
    api_key    TEXT    NOT NULL DEFAULT '',
    categories TEXT    NOT NULL DEFAULT '7000,7020',
    enabled    INTEGER NOT NULL DEFAULT 1,
    priority   INTEGER NOT NULL DEFAULT 25,
    added_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);
