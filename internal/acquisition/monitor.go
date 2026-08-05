package acquisition

import (
	"context"
	"fmt"
	"time"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
)

// MonitorArtist starts watching mbid: looks it up on MusicBrainz, records
// it as a MonitoredArtist, and seeds its wanted albums (see
// syncWantedAlbums) in the same step.
func (s *Service) MonitorArtist(ctx context.Context, mbid string) (*database.MonitoredArtist, error) {
	artist, err := s.mb.LookupArtist(ctx, mbid)
	if err != nil {
		return nil, fmt.Errorf("look up artist %s: %w", mbid, err)
	}

	m, err := s.db.CreateMonitoredArtist(ctx, mbid, artist.Name, artist.SortName)
	if err != nil {
		return nil, fmt.Errorf("create monitored artist: %w", err)
	}

	if err := s.syncWantedAlbums(ctx, m.ID, artist); err != nil {
		return m, fmt.Errorf("seed wanted albums: %w", err)
	}
	return m, nil
}

// SyncArtist re-fetches monitoredArtistID's release groups from
// MusicBrainz and wants any new ones — an artist can release a new album
// after being monitored, and this is how CantiNode notices. Existing
// wanted albums are left exactly as they are (see GetOrCreateWantedAlbum
// — already-downloaded or user-ignored ones are never reset back to
// "wanted" by a re-sync).
func (s *Service) SyncArtist(ctx context.Context, monitoredArtistID int64) error {
	m, err := s.db.GetMonitoredArtist(ctx, monitoredArtistID)
	if err != nil {
		return fmt.Errorf("get monitored artist: %w", err)
	}
	artist, err := s.mb.LookupArtist(ctx, m.MBID)
	if err != nil {
		return fmt.Errorf("look up artist %s: %w", m.MBID, err)
	}
	if err := s.syncWantedAlbums(ctx, m.ID, artist); err != nil {
		return err
	}
	return s.db.SetMonitoredArtistSynced(ctx, m.ID, time.Now().UTC())
}

// syncWantedAlbums wants every one of artist's release groups that's a
// plain studio album — PrimaryType "Album" with no secondary types
// (Live/Compilation/Remix/...). Deliberately narrower than "everything
// this artist ever released": that's what an operator monitoring an
// artist actually means in the overwhelming majority of cases, and
// broader monitoring (EPs, singles, live albums) is a natural future
// setting rather than the v1 default — see ROADMAP.md.
func (s *Service) syncWantedAlbums(ctx context.Context, monitoredArtistID int64, artist *musicbrainz.Artist) error {
	for _, rg := range artist.ReleaseGroups {
		if rg.PrimaryType != "Album" || len(rg.SecondaryTypes) > 0 {
			continue
		}
		if _, err := s.db.GetOrCreateWantedAlbum(ctx, monitoredArtistID, rg.ID, rg.Title, rg.PrimaryType, rg.FirstReleaseDate); err != nil {
			return fmt.Errorf("want release group %s: %w", rg.ID, err)
		}
	}
	return nil
}

// SearchArtists proxies a fuzzy artist search to MusicBrainz — lets the
// "monitor an artist" UI resolve a plain-text name to an MBID before
// calling MonitorArtist.
func (s *Service) SearchArtists(ctx context.Context, name string) ([]musicbrainz.Artist, error) {
	return s.mb.SearchArtists(ctx, name)
}

// UnmonitorArtist stops watching monitoredArtistID — its wanted albums
// (and any in-flight downloads for them) cascade-delete with it. Nothing
// already imported into the library is affected; this only touches the
// acquisition-tracking tables.
func (s *Service) UnmonitorArtist(ctx context.Context, monitoredArtistID int64) error {
	return s.db.DeleteMonitoredArtist(ctx, monitoredArtistID)
}

// IgnoreWantedAlbum marks a wanted album as ignored — CantiNode stops
// surfacing it for search/grab, without unmonitoring the whole artist.
func (s *Service) IgnoreWantedAlbum(ctx context.Context, wantedAlbumID int64) error {
	return s.db.SetWantedAlbumStatus(ctx, wantedAlbumID, database.WantedStatusIgnored)
}
