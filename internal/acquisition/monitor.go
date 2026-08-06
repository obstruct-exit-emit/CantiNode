package acquisition

import (
	"context"
	"fmt"
	"time"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
)

// MonitorArtist starts watching mbid: looks it up on MusicBrainz,
// upserts its Artist row (idempotent — works whether or not it already
// owns files, see database.GetOrCreateArtist), flips IsMonitored on, and
// caches its full discography plus a best-effort bio/image fetch (see
// refreshDiscographyAndMetadata). Deliberately does not seed any
// wanted_albums itself — unlike the old MonitoredArtist flow, monitoring
// no longer auto-wants anything; the user picks what to want from the
// cached discography via AddWantedAlbum (see the unified artist page's
// "Missing" section).
func (s *Service) MonitorArtist(ctx context.Context, mbid string) (*database.Artist, error) {
	mbArtist, err := s.mb.LookupArtist(ctx, mbid)
	if err != nil {
		return nil, fmt.Errorf("look up artist %s: %w", mbid, err)
	}

	a, err := s.db.GetOrCreateArtist(ctx, mbid, mbArtist.Name, mbArtist.SortName)
	if err != nil {
		return nil, fmt.Errorf("get or create artist: %w", err)
	}
	if err := s.db.SetArtistMonitored(ctx, a.ID, true); err != nil {
		return a, fmt.Errorf("set artist monitored: %w", err)
	}
	if err := s.refreshDiscographyAndMetadata(ctx, a.ID, mbArtist); err != nil {
		return a, err
	}
	return s.db.GetArtist(ctx, a.ID)
}

// RefreshArtistMetadata re-fetches artistID's discography and bio/image
// from MusicBrainz/TheAudioDB — what the unified artist page's "Refresh
// metadata" button calls, and also what picks up a newly released album
// for an artist that's been monitored for a while (there's no longer a
// separate "sync" concept; a refresh does both in one pass). Works
// whether or not artistID is currently monitored.
func (s *Service) RefreshArtistMetadata(ctx context.Context, artistID int64) error {
	a, err := s.db.GetArtist(ctx, artistID)
	if err != nil {
		return fmt.Errorf("get artist: %w", err)
	}
	mbArtist, err := s.mb.LookupArtist(ctx, a.MBID)
	if err != nil {
		return fmt.Errorf("look up artist %s: %w", a.MBID, err)
	}
	return s.refreshDiscographyAndMetadata(ctx, artistID, mbArtist)
}

// refreshDiscographyAndMetadata caches mbArtist's entire release-group
// list (any primary/secondary type — the unified page's "Missing"
// section lets the user pick, rather than CantiNode silently deciding
// only plain studio albums count) and best-effort fetches bio/image from
// TheAudioDB. A TheAudioDB failure (including simply not having this
// artist) is logged/recorded, never fatal — MonitorArtist/
// RefreshArtistMetadata must still succeed on the MusicBrainz side alone.
func (s *Service) refreshDiscographyAndMetadata(ctx context.Context, artistID int64, mbArtist *musicbrainz.Artist) error {
	groups := make([]database.ReleaseGroupCache, 0, len(mbArtist.ReleaseGroups))
	for _, rg := range mbArtist.ReleaseGroups {
		groups = append(groups, database.ReleaseGroupCache{
			ReleaseGroupMBID: rg.ID,
			Title:            rg.Title,
			PrimaryType:      rg.PrimaryType,
			SecondaryTypes:   rg.SecondaryTypes,
			FirstReleaseDate: rg.FirstReleaseDate,
		})
	}
	if err := s.db.ReplaceArtistReleaseGroups(ctx, artistID, groups); err != nil {
		return fmt.Errorf("cache discography: %w", err)
	}
	now := time.Now().UTC()
	if err := s.db.SetArtistSynced(ctx, artistID, now); err != nil {
		return fmt.Errorf("set artist synced: %w", err)
	}

	meta, err := s.getAudioDB().LookupArtistByMBID(ctx, mbArtist.ID)
	if err != nil {
		s.logger.Warn("acquisition: TheAudioDB lookup failed, continuing without bio/image", "artist_id", artistID, "mbid", mbArtist.ID, "error", err)
		return nil
	}
	var bio, imageURL string
	if meta != nil {
		bio, imageURL = meta.Bio, meta.ImageURL
	}
	if err := s.db.SetArtistMetadata(ctx, artistID, bio, imageURL, now); err != nil {
		return fmt.Errorf("set artist metadata: %w", err)
	}
	return nil
}

// SearchArtists proxies a fuzzy artist search to MusicBrainz — lets the
// "monitor an artist" UI resolve a plain-text name to an MBID before
// calling MonitorArtist.
func (s *Service) SearchArtists(ctx context.Context, name string) ([]musicbrainz.Artist, error) {
	return s.mb.SearchArtists(ctx, name)
}

// UnmonitorArtist stops actively tracking artistID — just flips
// IsMonitored off. Unlike the old MonitoredArtist flow, this does NOT
// delete the artist row, its owned albums, or its wanted_albums: an
// artist can own real library content independent of monitoring, and a
// never-grabbed wanted album simply stops showing as "actively
// monitored" rather than vanishing outright.
func (s *Service) UnmonitorArtist(ctx context.Context, artistID int64) error {
	return s.db.SetArtistMonitored(ctx, artistID, false)
}

// AddWantedAlbum wants releaseGroupMBID for artistID, looked up from the
// artist's own cached discography (artist_release_groups) rather than a
// fresh MusicBrainz call — the whole point of caching the discography up
// front is that every "Add"/"Add & Monitor" click in the UI is instant.
// Fails if releaseGroupMBID isn't in the cache yet (the artist hasn't
// been monitored/refreshed). Never touches IsMonitored itself — "Add &
// Monitor" is this call plus a separate SetArtistMonitored, left to the
// caller (internal/api) so a plain "Add" can't accidentally start
// monitoring the artist as a side effect.
func (s *Service) AddWantedAlbum(ctx context.Context, artistID int64, releaseGroupMBID string) (*database.WantedAlbum, error) {
	groups, err := s.db.ListArtistReleaseGroups(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("list artist release groups: %w", err)
	}
	for _, rg := range groups {
		if rg.ReleaseGroupMBID == releaseGroupMBID {
			w, err := s.db.GetOrCreateWantedAlbum(ctx, artistID, rg.ReleaseGroupMBID, rg.Title, rg.PrimaryType, rg.FirstReleaseDate)
			if err != nil {
				return nil, fmt.Errorf("want release group: %w", err)
			}
			return w, nil
		}
	}
	return nil, fmt.Errorf("acquisition: release group %s is not in artist %d's cached discography — monitor or refresh the artist first", releaseGroupMBID, artistID)
}

// IgnoreWantedAlbum marks a wanted album as ignored — CantiNode stops
// surfacing it for search/grab, without unmonitoring the whole artist.
func (s *Service) IgnoreWantedAlbum(ctx context.Context, wantedAlbumID int64) error {
	return s.db.SetWantedAlbumStatus(ctx, wantedAlbumID, database.WantedStatusIgnored)
}
