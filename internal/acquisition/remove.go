package acquisition

import (
	"context"
	"fmt"

	"github.com/cantinode/cantinode/internal/database"
)

// RemoveArtist removes artistID from CantiNode entirely — the artist
// page's danger "Remove artist" action. In order:
//
//  1. Every one of the artist's own track files is either deleted from
//     disk (deleteFiles=true, via scanner.Scanner.DeleteTrackFile — same
//     already-missing-tolerant removal a single-file delete uses) or
//     unlinked back to unmatched (deleteFiles=false, via scanner.Scanner.
//     ClearMatch) so the file stays on disk untouched and is picked up as
//     a plain unmatched file on the next scan, rather than left as a
//     ghost row pointing at a track that's about to stop existing.
//  2. Every in-flight ("downloading") download tied to one of the
//     artist's wanted albums is best-effort canceled (see CancelDownload)
//     so a torrent/nzb doesn't keep running orphaned forever.
//  3. The artist row itself is deleted, cascading (per the schema's own
//     FK setup) to albums/tracks, wanted_albums/downloads, and cached
//     discography.
//
// Deliberately application-level rather than a raw "DELETE FROM artists":
// track_files.track_id is ON DELETE SET NULL, not CASCADE, so skipping
// step 1 would silently orphan every track_files row — track_id goes NULL
// but match_status stays whatever it was, e.g. still 'matched' with
// nothing left to point at. See database.DeleteArtist's own doc comment
// for the precedent this mirrors (migrations/0004_unified_artist.sql hit
// the identical class of bug for wanted_albums/downloads).
func (s *Service) RemoveArtist(ctx context.Context, artistID int64, deleteFiles bool) error {
	files, err := s.db.ListTrackFilesByArtist(ctx, artistID)
	if err != nil {
		return fmt.Errorf("list track files by artist: %w", err)
	}
	for _, tf := range files {
		if deleteFiles {
			if err := s.scanner.DeleteTrackFile(ctx, tf.ID); err != nil {
				return fmt.Errorf("delete track file %d: %w", tf.ID, err)
			}
			continue
		}
		if err := s.scanner.ClearMatch(ctx, tf.ID); err != nil {
			return fmt.Errorf("clear match for track file %d: %w", tf.ID, err)
		}
	}

	wanted, err := s.db.ListWantedAlbumsByArtist(ctx, artistID)
	if err != nil {
		return fmt.Errorf("list wanted albums by artist: %w", err)
	}
	wantedIDs := make(map[int64]bool, len(wanted))
	for _, w := range wanted {
		wantedIDs[w.ID] = true
	}
	downloading, err := s.db.ListDownloadsByStatus(ctx, database.DownloadStatusDownloading)
	if err != nil {
		return fmt.Errorf("list downloading downloads: %w", err)
	}
	for _, d := range downloading {
		if !wantedIDs[d.WantedAlbumID] {
			continue
		}
		if err := s.CancelDownload(ctx, d.ID); err != nil {
			s.logger.Warn("acquisition: failed to cancel in-flight download while removing artist", "artist_id", artistID, "download_id", d.ID, "error", err)
		}
	}

	if err := s.db.DeleteArtist(ctx, artistID); err != nil {
		return fmt.Errorf("delete artist: %w", err)
	}
	return nil
}
