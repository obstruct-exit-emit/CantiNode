-- Album identity is the MusicBrainz release GROUP (the canonical album),
-- not a specific release (one pressing/edition of it). Before this
-- migration, GetOrCreateAlbum deduplicated by mbid alone, so two tracks of
-- the same physical album that happened to resolve to two different
-- release editions (see musicbrainz.Recording.BestRelease) would each get
-- their own albums row — one album splitting into several library cards.
--
-- Any install that hit that bug before upgrading can already have more
-- than one row per (artist_id, release_group_mbid), which would make the
-- unique index below fail to create. Collapse those down to one row per
-- release group first, keeping whichever was recorded first (lowest id)
-- — the same "first recorded wins" idiom GetOrCreateAlbum itself follows
-- on a repeat call. tracks.album_id's ON DELETE CASCADE (0001_init.sql)
-- removes the deleted rows' now-orphaned tracks along with them; any
-- track_files that pointed at those tracks fall back to track_id = NULL
-- (ON DELETE SET NULL) rather than vanishing — landing back in the
-- unmatched review queue, where a rescan re-attaches them to the one
-- surviving album row.
DELETE FROM albums
WHERE release_group_mbid != ''
  AND id NOT IN (
      SELECT MIN(id) FROM albums
      WHERE release_group_mbid != ''
      GROUP BY artist_id, release_group_mbid
  );

-- release_group_mbid is never empty in practice (it always comes from
-- MusicBrainz's own release.ReleaseGroup.ID), but the WHERE clause keeps
-- this index from rejecting a hypothetical future row that legitimately
-- has none rather than corrupting inserts. albums.mbid keeps its existing
-- global UNIQUE constraint (0001_init.sql): with this migration in place,
-- GetOrCreateAlbum only ever inserts one row per (artist, release group),
-- so the specific release mbid it records can never collide with another
-- row's — the two constraints don't conflict.
CREATE UNIQUE INDEX idx_albums_artist_release_group
    ON albums(artist_id, release_group_mbid)
    WHERE release_group_mbid != '';
