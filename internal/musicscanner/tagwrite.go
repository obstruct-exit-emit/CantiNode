package musicscanner

import (
	"fmt"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagwriter"
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

	// track.ArtistCredit is only ever non-empty when it differs from the
	// album's own artist (see applyMatch) — the real per-recording
	// performer on a Various Artists compilation. Writing artist.Name
	// there would stamp every track's ARTIST tag with "Various Artists",
	// discarding exactly the distinction this field exists to preserve.
	trackArtist := artist.Name
	if track.ArtistCredit != "" {
		trackArtist = track.ArtistCredit
	}

	tags := tagwriter.Tags{
		Title:                     track.Title,
		Artist:                    trackArtist,
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

// WriteTagsForAlbum runs WriteTags for every matched file belonging to
// albumID — the album page's own "Write tags" action, now that per-file
// buttons are gone (write-tags/organize/delete are all bulk, album- or
// artist-scoped actions; a single stray file is the rare exception, not
// the common case this UI should optimize for). An unmatched file is
// silently skipped (nothing to write), the same tolerance
// PlanOrganizeAlbum already has; any other per-file failure is recorded
// in errs and does not stop the rest, mirroring applyOrganizePlan's own
// non-aborting pattern.
func (s *Scanner) WriteTagsForAlbum(albumID int64) (written int, errs []string, err error) {
	files, err := s.db.ListTrackFilesByAlbum(albumID)
	if err != nil {
		return 0, nil, fmt.Errorf("list track files by album: %w", err)
	}
	return s.writeTagsForFiles(files)
}

// WriteTagsForArtist is WriteTagsForAlbum scoped to every album artistID
// owns — the artist page's own "Write tags" action.
func (s *Scanner) WriteTagsForArtist(artistID int64) (written int, errs []string, err error) {
	files, err := s.db.ListTrackFilesByArtist(artistID)
	if err != nil {
		return 0, nil, fmt.Errorf("list track files by artist: %w", err)
	}
	return s.writeTagsForFiles(files)
}

func (s *Scanner) writeTagsForFiles(files []musiclibrary.TrackFile) (written int, errs []string, err error) {
	errs = []string{}
	for _, tf := range files {
		if tf.MatchStatus == musiclibrary.StatusUnmatched {
			continue
		}
		if werr := s.WriteTags(tf.ID); werr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tf.Path, werr))
			continue
		}
		written++
	}
	return written, errs, nil
}
