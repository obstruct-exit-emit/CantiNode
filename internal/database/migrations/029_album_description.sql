-- Album description, sourced from TheAudioDB (the only provider CantiNode
-- has for this — MusicBrainz release/release-group data has no
-- description-style field). Cached the same way artists.bio is: fetched
-- once, on first request, then never re-fetched — description_fetched_at
-- distinguishes "never tried" (NULL) from "tried, TheAudioDB had nothing"
-- (non-NULL, description still '') the same way artists.metadata_fetched_at
-- does, so a miss isn't retried on every page view.
ALTER TABLE albums ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE albums ADD COLUMN description_fetched_at TIMESTAMP;
