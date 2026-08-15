-- MusicBrainz already returns each track's own performing-artist credit
-- in the same tracklist response CantiNode already fetches
-- (release.media[].tracks[].recording.artist-credit) — confirmed live
-- against a real "Various Artists" compilation, where each track's own
-- credit correctly differs (Phil Collins, Duran Duran, UB40, ...) from
-- the release's own "Various Artists" credit. Nothing stored it before
-- now. Plain display text, not a structured relation — nothing queries
-- by it, it just needs to be shown.
ALTER TABLE tracks ADD COLUMN artist_credit TEXT NOT NULL DEFAULT '';
