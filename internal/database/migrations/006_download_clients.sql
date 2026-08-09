-- Download clients: qBittorrent (torrents) and SABnzbd (usenet). category is
-- the client-side label/folder LibriNode claims for its downloads; priority
-- picks between multiple clients of the same protocol (lower wins).

CREATE TABLE download_clients (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT    NOT NULL UNIQUE,
    type     TEXT    NOT NULL CHECK (type IN ('qbittorrent', 'sabnzbd')),
    host     TEXT    NOT NULL,
    username TEXT    NOT NULL DEFAULT '',
    password TEXT    NOT NULL DEFAULT '',
    api_key  TEXT    NOT NULL DEFAULT '',
    category TEXT    NOT NULL DEFAULT 'librinode',
    enabled  INTEGER NOT NULL DEFAULT 1,
    priority INTEGER NOT NULL DEFAULT 1,
    added_at TEXT    NOT NULL DEFAULT (datetime('now'))
);
