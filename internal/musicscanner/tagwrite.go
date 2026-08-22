package musicscanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagwriter"
)

// WriteTags embeds trackFileID's matched metadata (artist/album/track
// names, track/disc numbers, genre, release type, sort names, track/disc
// totals, release country/status/media, mood, composer, cover art, and
// every MusicBrainz ID CantiNode already resolved for it) back into the
// file's own tags — see internal/tagwriter's package doc comment for exactly
// which formats this supports. Requires the file to already be matched;
// there's nothing to write otherwise. clear requests a full wipe of
// everything this function doesn't itself set, rather than the default
// merge — see tagwriter.Write's own doc comment for what that actually
// destroys and why it's opt-in. Which of the fields above actually get
// written is further gated by the live tagToggles setting (Settings →
// Music → "Tags to write") — see Scanner.getTagToggles.
func (s *Scanner) WriteTags(ctx context.Context, trackFileID int64, clear bool) error {
	return s.writeTagsForFile(ctx, trackFileID, clear, map[int64][]byte{})
}

// writeTagsForFile is WriteTags's own body, taking a cover-image cache
// shared across a whole bulk operation (WriteTagsForAlbum/WriteTagsForArtist)
// so a release's cover is fetched at most once per call, not once per
// track file — coverart.Client.GetFrontCover already disk-caches across
// separate calls, but a bulk write for a 12-track album has no reason to
// even repeat that many cache reads plus re-decoding the same bytes.
func (s *Scanner) writeTagsForFile(ctx context.Context, trackFileID int64, clear bool, coverCache map[int64][]byte) error {
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

	// Best-effort: the version cache is fetched lazily (the album page's
	// own edition picker, first view onward), so it may genuinely not
	// exist yet for this release group, or not yet include this specific
	// release. Either way, leaving country/status/media blank (via
	// setFieldIfPresent) is correct — there's nothing wrong to report, just
	// nothing cached yet to write.
	var releaseCountry, releaseStatus, media string
	if v, err := s.db.GetReleaseGroupVersionByRelease(album.ReleaseGroupMBID, album.MBID); err == nil {
		releaseCountry, releaseStatus, media = v.Country, v.Status, v.MediaSummary
	} else if !errors.Is(err, musiclibrary.ErrNotFound) {
		return fmt.Errorf("get release version: %w", err)
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
		ReleaseCountry:            releaseCountry,
		ReleaseStatus:             releaseStatus,
		Media:                     media,
		Mood:                      album.Mood,
		Composer:                  track.Composer,
		CoverImage:                s.coverImageForAlbum(ctx, album, coverCache),
		MusicBrainzArtistID:       trackArtistMBID,
		AlbumArtistID:             artist.MBID,
		MusicBrainzAlbumID:        album.MBID,
		MusicBrainzReleaseGroupID: album.ReleaseGroupMBID,
		MusicBrainzRecordingID:    track.MBID,
	}
	if err := tagwriter.Write(tf.Path, tags, clear, s.getTagToggles()); err != nil {
		return fmt.Errorf("write tags to %s: %w", tf.Path, err)
	}
	return nil
}

// coverImageForAlbum returns album's own front cover image bytes, ready to
// embed — best-effort: no coverart.Client (tests), no cover available for
// this release (not yet cached and TheAudioDB/Cover Art Archive both come
// up empty), or a transient fetch failure all just mean nothing gets
// embedded, the same "cosmetic, not fatal" tolerance every other
// TheAudioDB/Cover Art Archive call in this codebase already has — a
// missing cover has never once blocked a match or a scan, and it doesn't
// block a tag write either. cache is keyed by album ID so a bulk write
// across one album's whole tracklist fetches/reads the cover once, not
// once per track file.
func (s *Scanner) coverImageForAlbum(ctx context.Context, album *musiclibrary.Album, cache map[int64][]byte) []byte {
	if s.coverart == nil {
		return nil
	}
	if img, ok := cache[album.ID]; ok {
		return img
	}
	var img []byte
	if path, _, err := s.coverart.GetFrontCover(ctx, album.ReleaseGroupMBID, album.MBID); err == nil {
		if data, rerr := os.ReadFile(path); rerr == nil {
			img = data
		}
	}
	cache[album.ID] = img
	return img
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
func (s *Scanner) WriteTagsForAlbum(ctx context.Context, albumID int64, clear bool) (written int, errs []string, err error) {
	files, err := s.db.ListTrackFilesByAlbum(albumID)
	if err != nil {
		return 0, nil, fmt.Errorf("list track files by album: %w", err)
	}
	return s.writeTagsForFiles(ctx, files, clear)
}

// WriteTagsForArtist is WriteTagsForAlbum scoped to every album artistID
// owns — the artist page's own "Write tags" action.
func (s *Scanner) WriteTagsForArtist(ctx context.Context, artistID int64, clear bool) (written int, errs []string, err error) {
	files, err := s.db.ListTrackFilesByArtist(artistID)
	if err != nil {
		return 0, nil, fmt.Errorf("list track files by artist: %w", err)
	}
	return s.writeTagsForFiles(ctx, files, clear)
}

func (s *Scanner) writeTagsForFiles(ctx context.Context, files []musiclibrary.TrackFile, clear bool) (written int, errs []string, err error) {
	coverCache := map[int64][]byte{}
	errs = []string{}
	for _, tf := range files {
		if tf.MatchStatus == musiclibrary.StatusUnmatched {
			continue
		}
		if werr := s.writeTagsForFile(ctx, tf.ID, clear, coverCache); werr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tf.Path, werr))
			continue
		}
		written++
	}
	return written, errs, nil
}
