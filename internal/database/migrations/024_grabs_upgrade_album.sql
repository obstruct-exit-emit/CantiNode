-- An "Upgrades allowed" grab (handleGrabAlbumUpgrade) has no wanted_albums
-- row to tie itself to — the album is already owned — so there was
-- previously no way for internal/importer to know, once such a grab
-- finishes importing and its files are matched back in, which album it was
-- an upgrade *for*. That's needed to swap the newly-matched (better) file
-- in for the one it supersedes instead of just leaving both on disk
-- forever. Mirrors wanted_album_id (021_grabs_wanted_album.sql): a plain
-- column, no enforced foreign key, so grab history survives the album
-- being removed later.
ALTER TABLE grabs ADD COLUMN upgrade_album_id INTEGER;
