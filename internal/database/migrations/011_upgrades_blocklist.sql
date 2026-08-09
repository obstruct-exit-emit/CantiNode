-- Phase 5: failed-release blocklist + quality upgrades.
-- grabs remember the release guid so a failure can blocklist precisely;
-- blocklisted releases are never grabbed again. Quality profiles gain an
-- upgrades switch: when on, owning a below-cutoff format keeps the book
-- wanted until the cutoff (empty cutoff = the profile's best format).

ALTER TABLE grabs ADD COLUMN guid TEXT NOT NULL DEFAULT '';
ALTER TABLE quality_profiles ADD COLUMN upgrades_allowed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN cutoff TEXT NOT NULL DEFAULT '';

CREATE TABLE blocklist (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    guid       TEXT    NOT NULL DEFAULT '',
    title      TEXT    NOT NULL,
    reason     TEXT    NOT NULL DEFAULT '',
    blocked_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_blocklist_guid ON blocklist (guid);
