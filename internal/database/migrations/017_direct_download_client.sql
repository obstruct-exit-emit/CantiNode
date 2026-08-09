-- The direct download client (LibriNode's own HTTP fetcher, the third
-- protocol beside torrent/usenet) is stored with type 'direct', which the
-- original CHECK constraint rejects. SQLite can't alter a CHECK, so the table
-- is rebuilt without it (foreign keys are off during migrations — grabs'
-- client_config_id reference survives untouched); the API validates the type
-- instead.

CREATE TABLE download_clients_new (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT    NOT NULL UNIQUE,
    type     TEXT    NOT NULL,
    host     TEXT    NOT NULL,
    username TEXT    NOT NULL DEFAULT '',
    password TEXT    NOT NULL DEFAULT '',
    api_key  TEXT    NOT NULL DEFAULT '',
    category TEXT    NOT NULL DEFAULT 'librinode',
    enabled  INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 1,
    added_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO download_clients_new (id, name, type, host, username, password, api_key, category, enabled, priority, added_at)
    SELECT id, name, type, host, username, password, api_key, category, enabled, priority, added_at
    FROM download_clients;

DROP TABLE download_clients;
ALTER TABLE download_clients_new RENAME TO download_clients;
