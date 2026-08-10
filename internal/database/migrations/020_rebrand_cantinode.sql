-- The project was renamed from LibriNode to CantiNode. The download_clients
-- table's category default was baked in as the literal string 'librinode' by
-- 006_download_clients.sql/017_direct_download_client.sql, which — like every
-- migration here — never re-runs on an existing database, so the rename has
-- to happen explicitly: rebuild the column default, and re-point any existing
-- row still holding the untouched default value (a customized category is
-- left alone).

CREATE TABLE download_clients_new (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT    NOT NULL UNIQUE,
    type     TEXT    NOT NULL,
    host     TEXT    NOT NULL,
    username TEXT    NOT NULL DEFAULT '',
    password TEXT    NOT NULL DEFAULT '',
    api_key  TEXT    NOT NULL DEFAULT '',
    category TEXT    NOT NULL DEFAULT 'cantinode',
    enabled  INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 1,
    added_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

INSERT INTO download_clients_new (id, name, type, host, username, password, api_key, category, enabled, priority, added_at)
    SELECT id, name, type, host, username, password, api_key,
           CASE WHEN category = 'librinode' THEN 'cantinode' ELSE category END,
           enabled, priority, added_at
    FROM download_clients;

DROP TABLE download_clients;
ALTER TABLE download_clients_new RENAME TO download_clients;
