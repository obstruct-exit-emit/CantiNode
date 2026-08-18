// Package discography re-caches an artist's (or a tracked series') own
// discography from MusicBrainz — the cheap, "did anything new appear"
// half of a full artist refresh, deliberately kept separate from the
// heavier per-release-group version/tracklist caching and TheAudioDB
// bio/photo fetch (both stay callers' own responsibility, see Refresh's
// own doc comment). Shared by internal/api's manual "Refresh metadata"/
// "Add artist"/"Add series" handlers and the periodic
// internal/discoveryrefresh sweep, so there's exactly one implementation
// of "what does re-caching a discography actually mean" for each kind.
package discography

import (
	"context"
	"time"

	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// Service ties a MusicBrainz client and the music store together for
// discography caching.
type Service struct {
	mb    *musicbrainz.Client
	store *musiclibrary.Store
}

func New(mb *musicbrainz.Client, store *musiclibrary.Store) *Service {
	return &Service{mb: mb, store: store}
}

// RefreshArtist stores mbArtist's full release-group discography (any
// primary/secondary type — the Missing section lets the user pick, fully
// paginated via BrowseArtistReleaseGroups rather than the truncated-at-25
// list a plain artist lookup returns) plus the genres/tags/rating that
// came back with the same lookup for free.
//
// mbArtist.ID == musicbrainz.VariousArtistsMBID is a deliberate exception:
// see that constant's own doc comment for why its "discography" is every
// compilation MusicBrainz has ever cataloged, not anything worth listing as
// Missing. Confirmed live: browsing it hit MusicBrainz's own rate limit
// partway through (10,000+ release groups, the highest CantiNode's own
// pagination ceiling allows) and had previously left just as many bogus
// "missing" rows cached from an earlier successful-but-pointless run —
// ReplaceArtistReleaseGroups(artistID, nil) clears those out the same way
// this skips creating any more of them, so any artist already affected
// self-heals the next time this runs (a scan, the metadata backfill sweep,
// or an explicit "Refresh metadata"), no manual cleanup needed.
func (s *Service) RefreshArtist(ctx context.Context, artistID int64, mbArtist *musicbrainz.Artist) ([]musiclibrary.ReleaseGroupCache, error) {
	if mbArtist.ID == musicbrainz.VariousArtistsMBID {
		if err := s.store.ReplaceArtistReleaseGroups(artistID, nil); err != nil {
			return nil, err
		}
		if err := s.store.SetArtistSynced(artistID, time.Now().UTC()); err != nil {
			return nil, err
		}
		return nil, nil
	}

	releaseGroups, err := s.mb.BrowseArtistReleaseGroups(ctx, mbArtist.ID)
	if err != nil {
		return nil, err
	}
	groups := make([]musiclibrary.ReleaseGroupCache, 0, len(releaseGroups))
	for _, rg := range releaseGroups {
		groups = append(groups, musiclibrary.ReleaseGroupCache{
			ReleaseGroupMBID: rg.ID,
			Title:            rg.Title,
			PrimaryType:      rg.PrimaryType,
			SecondaryTypes:   rg.SecondaryTypes,
			FirstReleaseDate: rg.FirstReleaseDate,
		})
	}
	if err := s.store.ReplaceArtistReleaseGroups(artistID, groups); err != nil {
		return nil, err
	}
	if err := s.store.SetArtistSynced(artistID, time.Now().UTC()); err != nil {
		return nil, err
	}

	genres := make([]string, 0, len(mbArtist.Genres))
	for _, g := range mbArtist.Genres {
		genres = append(genres, g.Name)
	}
	tags := make([]string, 0, len(mbArtist.Tags))
	for _, t := range mbArtist.Tags {
		tags = append(tags, t.Name)
	}
	if err := s.store.SetArtistMusicBrainzMetadata(artistID, genres, tags, mbArtist.Rating.Value, mbArtist.Rating.VotesCount); err != nil {
		return nil, err
	}
	return groups, nil
}

// RefreshSeries stores series' release-group membership as artistID's own
// discography — the series counterpart to RefreshArtist, reusing the same
// ReplaceArtistReleaseGroups/SetArtistSynced primitives directly rather
// than anything artist-specific (BrowseArtistReleaseGroups/genre-tag
// caching don't apply to a series).
func (s *Service) RefreshSeries(ctx context.Context, artistID int64, series *musicbrainz.Series) ([]musiclibrary.ReleaseGroupCache, error) {
	groups := make([]musiclibrary.ReleaseGroupCache, 0, len(series.Relations))
	for _, rel := range series.Relations {
		groups = append(groups, musiclibrary.ReleaseGroupCache{
			ReleaseGroupMBID: rel.ReleaseGroupMBID,
			Title:            rel.Title,
			PrimaryType:      rel.PrimaryType,
			SecondaryTypes:   rel.SecondaryTypes,
			FirstReleaseDate: rel.FirstReleaseDate,
		})
	}
	if err := s.store.ReplaceArtistReleaseGroups(artistID, groups); err != nil {
		return nil, err
	}
	if err := s.store.SetArtistSynced(artistID, time.Now().UTC()); err != nil {
		return nil, err
	}
	return groups, nil
}

// Refresh is the kind-branching entry point for a caller with nothing
// already looked up — just an artist row (real or a tracked series),
// which is all internal/discoveryrefresh's periodic sweep ever has in
// hand. Looks the artist up fresh via LookupArtist or LookupSeries
// (artist.Kind decides which, mirroring internal/api's own
// handleRefreshMusicArtist branch) and delegates to RefreshArtist/
// RefreshSeries — one MusicBrainz request in the common case.
//
// Deliberately stops there: never touches bio/photo (TheAudioDB) or
// per-release-group version/tracklist caching, both of which cost far
// more (a further MusicBrainz request per release group, per version) and
// aren't needed to answer "did a new release appear" — those stay on
// their existing manual-refresh/backfill-sweep paths so this stays cheap
// enough to run unconditionally across every monitored artist on a timer.
func (s *Service) Refresh(ctx context.Context, artist *musiclibrary.Artist) error {
	if artist.Kind == "series" {
		series, err := s.mb.LookupSeries(ctx, artist.MBID)
		if err != nil {
			return err
		}
		_, err = s.RefreshSeries(ctx, artist.ID, series)
		return err
	}
	mbArtist, err := s.mb.LookupArtist(ctx, artist.MBID)
	if err != nil {
		return err
	}
	_, err = s.RefreshArtist(ctx, artist.ID, mbArtist)
	return err
}
