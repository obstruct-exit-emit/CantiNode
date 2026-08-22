-- Composer/writer credit, resolved from MusicBrainz's work-level
-- relationships (a work's composer/writer artist-rel, reached via a
-- recording's own work-rel — see musicbrainz.Recording.Composer). Only
-- ever populated via a direct recording/release lookup; a file matched
-- through the batched recording-search path (BatchLookupRecordings) is
-- left blank here, since MusicBrainz's search endpoint never returns
-- relationship data regardless of inc params (confirmed live) and paying
-- for a second per-track lookup just for composer would defeat the
-- point of batching.
ALTER TABLE tracks ADD COLUMN composer TEXT NOT NULL DEFAULT '';
