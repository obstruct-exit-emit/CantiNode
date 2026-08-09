-- Quality profiles: per-media-type rules for which release formats are
-- acceptable and preferred. formats is an ordered comma list, best first;
-- exactly one profile per media type should be the default (enforced in the
-- store, not the schema). A "Standard Ebook" default ships out of the box.

CREATE TABLE quality_profiles (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL UNIQUE,
    media_type   TEXT    NOT NULL DEFAULT 'ebook'
                 CHECK (media_type IN ('ebook', 'audiobook', 'manga', 'comic')),
    formats      TEXT    NOT NULL,
    language     TEXT    NOT NULL DEFAULT 'english',
    retail_bonus INTEGER NOT NULL DEFAULT 25,
    min_size     INTEGER NOT NULL DEFAULT 20480,     -- 20 KiB
    max_size     INTEGER NOT NULL DEFAULT 524288000, -- 500 MiB
    is_default   INTEGER NOT NULL DEFAULT 0,
    added_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO quality_profiles (name, media_type, formats, is_default)
VALUES ('Standard Ebook', 'ebook', 'epub,azw3,mobi,pdf', 1);
