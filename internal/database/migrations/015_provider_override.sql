-- Per-author and per-series metadata provider override. Empty ('') — the
-- default — means "follow Settings → Metadata"; a provider name pins this
-- record to that provider, overriding the global selection (including a
-- global "none": libraries always honor the settings, and the override is
-- the explicit per-record escape hatch).

ALTER TABLE authors ADD COLUMN provider_override TEXT NOT NULL DEFAULT '';
ALTER TABLE series  ADD COLUMN provider_override TEXT NOT NULL DEFAULT '';
