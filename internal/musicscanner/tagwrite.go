package musicscanner

import (
	"fmt"
	"strings"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagwriter"
)

// WriteTags embeds trackFileID's matched metadata (artist/album/track
// names, track/disc numbers, genre, release type, sort names, track/disc
// totals, and every MusicBrainz ID CantiNode already resolved for it)
// back into the file's own tags — see internal/tagwriter's package doc
// comment for exactly which formats this supports. Requires the file to
// already be matched; there's nothing to write otherwise.
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
	siblings, err := s.db.ListTracksByAlbum(album.ID)
	if err != nil {
		return fmt.Errorf("list tracks by album: %w", err)
	}

	// track.ArtistCredit/ArtistCreditMBID are only ever non-empty when
	// they differ from the album's own artist (see applyMatch) — the real
	// per-recording performer, and that performer's own MusicBrainz ID, on
	// a Various Artists compilation. Writing artist.Name/artist.MBID there
	// would stamp every track with "Various Artists" and its ID, discarding
	// exactly the distinction these fields exist to preserve — found live:
	// the ID half of this used to always be artist.MBID regardless, so a
	// compilation track's ARTIST tag correctly named its real performer
	// while the ID tag right next to it silently disagreed.
	trackArtist := artist.Name
	if track.ArtistCredit != "" {
		trackArtist = track.ArtistCredit
	}
	trackArtistMBID := artist.MBID
	if track.ArtistCreditMBID != "" {
		trackArtistMBID = track.ArtistCreditMBID
	}

	// Only ever set when trackArtist is genuinely the album's own artist:
	// CantiNode has no stored sort name for a Various Artists track's own
	// distinct real performer (only ArtistCredit's display text and its
	// MBID), so writing the album artist's sort name there would
	// misattribute it the same way the old ID mismatch did.
	var artistSortName string
	if trackArtist == artist.Name {
		artistSortName = artist.SortName
	}

	// discTotal/trackTotal describe the whole release from its own
	// tracklist (siblings), not just this one track's own knowledge of
	// itself — trackTotal is scoped to this track's own disc, matching how
	// {DiscNumber}/{TrackNumber} already read per-disc in the naming
	// templates.
	var discTotal, trackTotal int
	for _, sib := range siblings {
		if sib.DiscNumber > discTotal {
			discTotal = sib.DiscNumber
		}
		if sib.DiscNumber == track.DiscNumber {
			trackTotal++
		}
	}

	var genre string
	if len(artist.Genres) > 0 {
		genre = strings.Join(artist.Genres, "; ")
	}

	tags := tagwriter.Tags{
		Title:                     track.Title,
		Artist:                    trackArtist,
		AlbumArtist:               artist.Name,
		Album:                     album.Title,
		TrackNumber:               track.TrackNumber,
		DiscNumber:                track.DiscNumber,
		TrackTotal:                trackTotal,
		DiscTotal:                 discTotal,
		Date:                      album.ReleaseDate,
		Genre:                     genre,
		ReleaseType:               album.PrimaryType,
		ArtistSortName:            artistSortName,
		AlbumArtistSortName:       artist.SortName,
		MusicBrainzArtistID:       trackArtistMBID,
		AlbumArtistID:             artist.MBID,
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
