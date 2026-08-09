package musicscanner

import (
	"fmt"

	"github.com/librinode/librinode/internal/tagwriter"
)

// WriteTags embeds trackFileID's matched metadata (artist/album/track
// names, track/disc numbers, and every MusicBrainz ID CantiNode already
// resolved for it) back into the file's own tags — see internal/
// tagwriter's package doc comment for exactly which formats this
// supports. Requires the file to already be matched; there's nothing to
// write otherwise.
func (s *Scanner) WriteTags(trackFileID int64) error {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return fmt.Errorf("get track file: %w", err)
	}
	if tf.TrackID == nil {
		return fmt.Errorf("track file %d is not matched, nothing to write", trackFileID)
	}

	track, err := s.db.GetTrack(*tf.TrackID)
	if err != nil {
		return fmt.Errorf("get track: %w", err)
	}
	album, err := s.db.GetAlbum(track.AlbumID)
	if err != nil {
		return fmt.Errorf("get album: %w", err)
	}
	artist, err := s.db.GetArtist(album.ArtistID)
	if err != nil {
		return fmt.Errorf("get artist: %w", err)
	}

	year := album.ReleaseDate
	if len(year) >= 4 {
		year = year[:4]
	}

	tags := tagwriter.Tags{
		Title:                     track.Title,
		Artist:                    artist.Name,
		AlbumArtist:               artist.Name,
		Album:                     album.Title,
		TrackNumber:               track.TrackNumber,
		DiscNumber:                track.DiscNumber,
		Year:                      year,
		MusicBrainzArtistID:       artist.MBID,
		MusicBrainzAlbumID:        album.MBID,
		MusicBrainzReleaseGroupID: album.ReleaseGroupMBID,
		MusicBrainzRecordingID:    track.MBID,
	}
	if err := tagwriter.Write(tf.Path, tags); err != nil {
		return fmt.Errorf("write tags to %s: %w", tf.Path, err)
	}
	return nil
}
