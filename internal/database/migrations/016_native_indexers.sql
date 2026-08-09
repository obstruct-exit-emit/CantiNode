-- Native indexers: built-in sources (e.g. AudioBook Bay) are stored in the
-- indexers table with their implementation name as `type`. The original CHECK
-- constraint only allowed 'newznab'/'torznab', so it must go. SQLite can't
-- alter a CHECK, so the table is rebuilt without it (foreign keys are off
-- during migrations); the API validates the type instead — a known API dialect
-- or a registered native implementation.

CREATE TABLE indexers_new (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT    NOT NULL UNIQUE,
    type                TEXT    NOT NULL,
    base_url            TEXT    NOT NULL DEFAULT '',
    api_key             TEXT    NOT NULL DEFAULT '',
    categories          TEXT    NOT NULL DEFAULT '7000,7020',
    audio_categories    TEXT    NOT NULL DEFAULT '3030',
    comic_categories    TEXT    NOT NULL DEFAULT '7030',
    magazine_categories TEXT    NOT NULL DEFAULT '7010',
    enabled             INTEGER NOT NULL DEFAULT 1,
    priority            INTEGER NOT NULL DEFAULT 25,
    added_at            TEXT    NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO indexers_new (id, name, type, base_url, api_key, categories, audio_categories, comic_categories, magazine_categories, enabled, priority, added_at)
    SELECT id, name, type, base_url, api_key, categories, audio_categories, comic_categories, magazine_categories, enabled, priority, added_at
    FROM indexers;

DROP TABLE indexers;
ALTER TABLE indexers_new RENAME TO indexers;
