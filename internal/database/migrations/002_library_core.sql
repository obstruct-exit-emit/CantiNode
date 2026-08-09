-- Library core: authors, series, books, editions.
-- foreign_id is the identifier at the metadata provider (metadata_source),
-- e.g. a Hardcover author/book/edition id. Local ids stay internal.

CREATE TABLE authors (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    metadata_source TEXT    NOT NULL DEFAULT 'hardcover',
    foreign_id      TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    sort_name       TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    image_url       TEXT    NOT NULL DEFAULT '',
    monitored       INTEGER NOT NULL DEFAULT 1,
    added_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (metadata_source, foreign_id)
);

CREATE TABLE series (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    metadata_source TEXT    NOT NULL DEFAULT 'hardcover',
    foreign_id      TEXT    NOT NULL,
    title           TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    UNIQUE (metadata_source, foreign_id)
);

CREATE TABLE books (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    author_id       INTEGER NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    metadata_source TEXT    NOT NULL DEFAULT 'hardcover',
    foreign_id      TEXT    NOT NULL,
    title           TEXT    NOT NULL,
    sort_title      TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    release_date    TEXT    NOT NULL DEFAULT '',
    rating          REAL    NOT NULL DEFAULT 0,
    cover_url       TEXT    NOT NULL DEFAULT '',
    monitored       INTEGER NOT NULL DEFAULT 1,
    added_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (metadata_source, foreign_id)
);

CREATE INDEX idx_books_author_id ON books (author_id);

-- position is REAL so "book 1.5" style entries sort correctly.
CREATE TABLE series_books (
    series_id INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    book_id   INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    position  REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, book_id)
);

CREATE INDEX idx_series_books_book_id ON series_books (book_id);

CREATE TABLE editions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id         INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    metadata_source TEXT    NOT NULL DEFAULT 'hardcover',
    foreign_id      TEXT    NOT NULL,
    title           TEXT    NOT NULL DEFAULT '',
    isbn13          TEXT    NOT NULL DEFAULT '',
    asin            TEXT    NOT NULL DEFAULT '',
    format          TEXT    NOT NULL DEFAULT 'unknown'
                    CHECK (format IN ('ebook', 'audiobook', 'physical', 'unknown')),
    publisher       TEXT    NOT NULL DEFAULT '',
    language        TEXT    NOT NULL DEFAULT '',
    release_date    TEXT    NOT NULL DEFAULT '',
    cover_url       TEXT    NOT NULL DEFAULT '',
    monitored       INTEGER NOT NULL DEFAULT 0,
    UNIQUE (metadata_source, foreign_id)
);

CREATE INDEX idx_editions_book_id ON editions (book_id);
