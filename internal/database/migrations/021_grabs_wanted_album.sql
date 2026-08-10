-- grabs.book_id was a leftover from the removed prose/comic library (see
-- 019_music_only.sql) — always 0 for a music grab, since nothing ever set
-- it. Renamed to what a music grab actually needs to track: the
-- wanted_albums row it was made for, so internal/importer can transition
-- that row's status (wanted -> downloading -> downloaded, or back to
-- wanted on failure) once the grab resolves, instead of leaving it stuck
-- at "downloading" forever with no way back. Like client_config_id, this
-- is a plain column with no enforced foreign key — grab history is meant
-- to survive its wanted_albums row being removed, not cascade with it.
ALTER TABLE grabs RENAME COLUMN book_id TO wanted_album_id;
