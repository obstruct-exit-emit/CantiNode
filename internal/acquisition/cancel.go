package acquisition

import (
	"context"
	"fmt"

	"github.com/cantinode/cantinode/internal/database"
)

// CancelDownload stops tracking a download the user no longer wants —
// e.g. grabbed the wrong release, or a torrent turned out to be dead.
// Best-effort removes it from whichever download client it was sent to
// (a failure there is logged, not fatal — the row still needs to go away
// either way), deletes its own row, and reverts the wanted album back to
// "wanted" so the user can search again, but only if nothing else has
// already moved it past "downloading" (e.g. a completed-but-not-yet-
// canceled download that PollDownloads already imported concurrently).
func (s *Service) CancelDownload(ctx context.Context, downloadID int64) error {
	d, err := s.db.GetDownload(ctx, downloadID)
	if err != nil {
		return fmt.Errorf("get download: %w", err)
	}

	switch d.Protocol {
	case database.ProtocolTorrent:
		if qbit := s.getQBittorrent(); qbit != nil {
			if err := qbit.Remove(ctx, d.ClientID); err != nil {
				s.logger.Warn("acquisition: failed to remove torrent from qBittorrent during cancel", "download_id", d.ID, "error", err)
			}
		}
	case database.ProtocolUsenet:
		if sab := s.getSABnzbd(); sab != nil {
			if err := sab.Remove(ctx, d.ClientID); err != nil {
				s.logger.Warn("acquisition: failed to remove nzb from SABnzbd during cancel", "download_id", d.ID, "error", err)
			}
		}
	}

	if err := s.db.DeleteDownload(ctx, downloadID); err != nil {
		return fmt.Errorf("delete download: %w", err)
	}

	wanted, err := s.db.GetWantedAlbum(ctx, d.WantedAlbumID)
	if err != nil {
		return fmt.Errorf("get wanted album: %w", err)
	}
	if wanted.Status == database.WantedStatusDownloading {
		if err := s.db.SetWantedAlbumStatus(ctx, d.WantedAlbumID, database.WantedStatusWanted); err != nil {
			return fmt.Errorf("revert wanted album status: %w", err)
		}
	}
	return nil
}
