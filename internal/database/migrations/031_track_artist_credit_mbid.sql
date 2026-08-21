-- ArtistCredit already stores a track's own real per-recording performer
-- as DISPLAY TEXT when it differs from the album's filing artist (a
-- Various Artists compilation's real "Phil Collins" alongside the
-- album's own "Various Artists"). Nothing stored the same distinction for
-- the corresponding MusicBrainz ID: every track's embedded "MusicBrainz
-- Artist Id" tag was written as the FILING artist's ID regardless, so a
-- compilation track's ARTIST tag correctly named its real performer while
-- the ID tag right next to it silently pointed at Various Artists instead.
ALTER TABLE tracks ADD COLUMN artist_credit_mbid TEXT NOT NULL DEFAULT '';
