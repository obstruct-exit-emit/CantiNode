package acquisition

import (
	"context"
	"fmt"

	"github.com/cantinode/cantinode/internal/prowlarr"
)

// SearchReleases searches Prowlarr for wantedAlbumID — the query is the
// monitored artist's name plus the wanted album's own title, which in
// practice is what actually finds the right release across arbitrary
// indexer naming conventions far more reliably than either alone.
func (s *Service) SearchReleases(ctx context.Context, wantedAlbumID int64) ([]prowlarr.Release, error) {
	pw := s.getProwlarr()
	if pw == nil {
		return nil, errProwlarrNotConfigured
	}

	w, err := s.db.GetWantedAlbum(ctx, wantedAlbumID)
	if err != nil {
		return nil, fmt.Errorf("get wanted album: %w", err)
	}
	m, err := s.db.GetMonitoredArtist(ctx, w.MonitoredArtistID)
	if err != nil {
		return nil, fmt.Errorf("get monitored artist: %w", err)
	}

	query := m.Name + " " + w.Title
	releases, err := pw.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search prowlarr: %w", err)
	}
	return releases, nil
}
