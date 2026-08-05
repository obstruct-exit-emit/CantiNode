package scanner

import (
	"context"
	"fmt"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// matchFile attempts to match tf against MusicBrainz using tags (already
// read from tf's file): a direct lookup if the file's own tags already
// carry a MusicBrainz recording ID, otherwise a fuzzy search accepted
// only above s.minMatchConfidence. Returns whether a match was applied —
// false (with a nil error) just means "left unmatched for manual
// review", not a failure.
func (s *Scanner) matchFile(ctx context.Context, tf *database.TrackFile, tags *tagreader.Tags) (bool, error) {
	if tags.MusicBrainzRecordingID != "" {
		rec, err := s.mb.LookupRecording(ctx, tags.MusicBrainzRecordingID)
		if err != nil {
			return false, fmt.Errorf("lookup recording %s: %w", tags.MusicBrainzRecordingID, err)
		}
		if err := s.applyMatch(ctx, tf, *rec, 1.0, tags.MusicBrainzAlbumID, tags.TrackNumber, tags.DiscNumber, database.StatusMatched); err != nil {
			return false, err
		}
		return true, nil
	}

	if tags.Artist == "" && tags.Title == "" {
		return false, nil
	}

	candidates, err := s.mb.SearchRecordings(ctx, tags.Artist, tags.Album, tags.Title)
	if err != nil {
		return false, fmt.Errorf("search recordings: %w", err)
	}
	if len(candidates) == 0 {
		return false, nil
	}

	best := candidates[0]
	confidence := float64(best.Score) / 100.0
	if confidence < s.getMinMatchConfidence() {
		return false, nil
	}

	if err := s.applyMatch(ctx, tf, best, confidence, "", tags.TrackNumber, tags.DiscNumber, database.StatusMatched); err != nil {
		return false, err
	}
	return true, nil
}

// applyMatch resolves rec's artist/release into artists/albums/tracks
// rows (creating them if this is the first file to reference them) and
// links tf to the resulting track.
//
// Track number/disc number come from the file's own tags, not
// MusicBrainz: a Recording on its own doesn't carry a track/disc
// position (that's a property of a specific release's specific medium,
// which would need a separate release lookup with inc=recordings to
// resolve). The file's own tags are what a ripper/tagger already placed
// there and are reliable in the overwhelming majority of real files, so
// v1 trusts them rather than adding a second MusicBrainz round-trip.
func (s *Scanner) applyMatch(ctx context.Context, tf *database.TrackFile, rec musicbrainz.Recording, confidence float64, preferredReleaseMBID string, trackNumber, discNumber int, status database.MatchStatus) error {
	artistRef := rec.PrimaryArtist()
	if artistRef.ID == "" {
		return fmt.Errorf("recording %s has no artist credit", rec.ID)
	}
	artist, err := s.db.GetOrCreateArtist(ctx, artistRef.ID, artistRef.Name, artistRef.SortName)
	if err != nil {
		return fmt.Errorf("get or create artist: %w", err)
	}

	release := rec.BestRelease(preferredReleaseMBID)
	if release.ID == "" {
		return fmt.Errorf("recording %s has no associated release", rec.ID)
	}
	album, err := s.db.GetOrCreateAlbum(ctx, artist.ID, release.ID, release.ReleaseGroup.ID, release.Title, release.Date, release.ReleaseGroup.PrimaryType)
	if err != nil {
		return fmt.Errorf("get or create album: %w", err)
	}

	if discNumber == 0 {
		discNumber = 1
	}
	track, err := s.db.GetOrCreateTrack(ctx, album.ID, rec.ID, rec.Title, trackNumber, discNumber, int64(rec.Length))
	if err != nil {
		return fmt.Errorf("get or create track: %w", err)
	}

	if err := s.db.SetTrackFileMatch(ctx, tf.ID, &track.ID, status, confidence); err != nil {
		return fmt.Errorf("set track file match: %w", err)
	}
	return nil
}

// SearchMusicBrainz proxies a fuzzy recording search to the MusicBrainz
// client — used by the manual-review UI/API to let a human search for the
// right recording themselves when an automatic match wasn't confident
// enough (or was wrong).
func (s *Scanner) SearchMusicBrainz(ctx context.Context, artist, album, title string) ([]musicbrainz.Recording, error) {
	return s.mb.SearchRecordings(ctx, artist, album, title)
}

// ManualMatch links trackFileID to the MusicBrainz recording recordingMBID
// directly — no confidence threshold, since a human picked it. Used by
// the review UI once the user has found the right recording (e.g. via
// SearchMusicBrainz).
func (s *Scanner) ManualMatch(ctx context.Context, trackFileID int64, recordingMBID, preferredReleaseMBID string) error {
	tf, err := s.db.GetTrackFile(ctx, trackFileID)
	if err != nil {
		return fmt.Errorf("get track file: %w", err)
	}

	rec, err := s.mb.LookupRecording(ctx, recordingMBID)
	if err != nil {
		return fmt.Errorf("lookup recording %s: %w", recordingMBID, err)
	}

	trackNumber, discNumber := 0, 1
	var tags *tagreader.Tags
	if t, err := tagreader.Read(tf.Path); err == nil {
		tags = t
	}
	if tags != nil {
		trackNumber, discNumber = tags.TrackNumber, tags.DiscNumber
	}

	return s.applyMatch(ctx, tf, *rec, 1.0, preferredReleaseMBID, trackNumber, discNumber, database.StatusManual)
}

// ClearMatch unlinks trackFileID from whatever track it was matched to,
// moving it back to unmatched — e.g. a manual match the user wants to
// undo. Does not delete the artist/album/track rows themselves, since
// other files may still reference them.
func (s *Scanner) ClearMatch(ctx context.Context, trackFileID int64) error {
	return s.db.SetTrackFileMatch(ctx, trackFileID, nil, database.StatusUnmatched, 0)
}
