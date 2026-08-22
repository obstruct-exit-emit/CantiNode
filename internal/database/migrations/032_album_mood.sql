-- Mood (e.g. "Trippy", "Melancholic") is TheAudioDB's own field, returned
-- by the exact same album lookup already fetched (once, lazily, on an
-- album's first page view) to cache Description — no new fetch, just
-- capturing a second field out of a response CantiNode already gets.
-- Reuses description_fetched_at as its own "have we tried" flag rather
-- than adding a second one: both fields come from the same TheAudioDB
-- call, at the same time, so there's nothing for a separate flag to
-- distinguish.
ALTER TABLE albums ADD COLUMN mood TEXT NOT NULL DEFAULT '';
