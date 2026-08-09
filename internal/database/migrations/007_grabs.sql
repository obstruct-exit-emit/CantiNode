-- Grab history: which releases were sent to a download client, for which
-- book, and what became of them. Completed Download Handling matches client
-- queue items back to these records (by client item id, or title when the
-- client doesn't report ids) to import finished files.

CREATE TABLE grabs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id          INTEGER REFERENCES books(id) ON DELETE SET NULL,
    client_config_id INTEGER REFERENCES download_clients(id) ON DELETE SET NULL,
    client_item_id   TEXT    NOT NULL DEFAULT '',
    title            TEXT    NOT NULL,
    protocol         TEXT    NOT NULL,
    status           TEXT    NOT NULL DEFAULT 'grabbed'
                     CHECK (status IN ('grabbed', 'imported', 'failed')),
    message          TEXT    NOT NULL DEFAULT '',
    grabbed_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);

CREATE INDEX idx_grabs_status ON grabs (status);
