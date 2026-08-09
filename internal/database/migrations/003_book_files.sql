-- Files found by library scans. book_id is NULL for files the scanner could
-- not match to a library book — those surface in the manual-import flow.
-- edition_id is reserved for ISBN/embedded-metadata matching (later phase).

CREATE TABLE book_files (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    root_folder_id  INTEGER NOT NULL REFERENCES root_folders(id) ON DELETE CASCADE,
    book_id         INTEGER REFERENCES books(id) ON DELETE SET NULL,
    edition_id      INTEGER REFERENCES editions(id) ON DELETE SET NULL,
    path            TEXT    NOT NULL UNIQUE,
    size            INTEGER NOT NULL DEFAULT 0,
    format          TEXT    NOT NULL DEFAULT '',
    modified_at     TEXT    NOT NULL DEFAULT '',
    added_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_book_files_book_id ON book_files (book_id);
CREATE INDEX idx_book_files_root_folder_id ON book_files (root_folder_id);
