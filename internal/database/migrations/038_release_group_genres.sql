-- Genres for a cached release group — MusicBrainz's own curated genre tags
-- (inc=genres), fetched alongside the rest of BrowseArtistReleaseGroups'
-- already-scheduled discography sync, never a separate live lookup.
-- Comma-joined, same convention as artists.genres/artists.tags.
ALTER TABLE artist_release_groups ADD COLUMN genres TEXT NOT NULL DEFAULT '';
