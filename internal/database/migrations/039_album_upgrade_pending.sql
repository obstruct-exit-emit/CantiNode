-- Lets handleGrabAlbumUpgrade claim an album atomically before grabbing,
-- the same shape ClaimWantedAlbumForDownload already gives wanted albums
-- — closing a real race where two rapid/duplicate upgrade requests for the
-- same album could both proceed to GrabRelease, creating two independent
-- GrabRecords whose swapUpgradedFiles "before" snapshots can interleave
-- and misidentify which old files were actually superseded. An owned
-- album has no wanted/downloading status to reuse, so this is a narrow
-- flag just for that one claim, not a general album status field.
ALTER TABLE albums ADD COLUMN upgrade_pending INTEGER NOT NULL DEFAULT 0;
