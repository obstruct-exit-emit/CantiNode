ALTER TABLE artists ADD COLUMN kind TEXT NOT NULL DEFAULT 'artist';
CREATE INDEX idx_artist_release_groups_release_group_mbid ON artist_release_groups (release_group_mbid);
