package musicscanner

import (
	"context"
	"fmt"
	"os"

	"github.com/librinode/librinode/internal/musicbrainz"
	"github.com/librinode/librinode/internal/musiclibrary"
	"github.com/librinode/librinode/internal/tagreader"
)

// matchFileDirect looks tf up directly by the MusicBrainz recording ID
// already embedded in its own tags — confidence 1.0. Bypasses all
// folder-level reasoning (see folder_match.go): the file's own tags are
// already as authoritative as MusicBrainz gets. Precondition:
// tags.MusicBrainzRecordingID != "".
func (s *Scanner) matchFileDirect(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags) (bool, error) {
	rec, err := s.mb.LookupRecording(ctx, tags.MusicBrainzRecordingID)
	if err != nil {
		return false, fmt.Errorf("lookup recording %s: %w", tags.MusicBrainzRecordingID, err)
	}
	if err := s.applyMatch(tf, *rec, 1.0, tags.MusicBrainzAlbumID, tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched); err != nil {
		return false, err
	}
	return true, nil
}

// matchFileFuzzy independently searches MusicBrainz for tf by its own
// artist/album/title tags — CantiNode's original per-file matching path.
// Kept as the safety-valve fallback matchFolder (folder_match.go) uses
// when a folder's files can't be confidently resolved to one common
// release (a genuinely standalone file, inconsistent tags, or a failed
// release search/lookup) — this is exactly the path that used to run for
// every file independently, which is how one album folder could end up
// split across several different release matches; it's still correct for
// a single isolated file, just no longer folder-grouping-aware itself.
func (s *Scanner) matchFileFuzzy(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags) (bool, error) {
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

	if err := s.applyMatch(tf, best, confidence, "", tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched); err != nil {
		return false, err
	}
	return true, nil
}

// applyMatch resolves rec's artist/release into artists/albums/tracks
// rows (creating them if this is the first file to reference them) and
// links tf to the resulting track.
//
// trackNumber/discNumber are supplied by the caller, not derived here: a
// Recording alone has no release-scoped position (that's a property of a
// specific release's specific medium). matchFileDirect/matchFileFuzzy pass
// the file's own tags (the best available signal for an isolated file);
// matchEntriesToRelease (folder_match.go) passes the position from the
// resolved release's own already-fetched tracklist, which is more
// authoritative than a file's own tags whenever a release has actually
// been resolved.
func (s *Scanner) applyMatch(tf *musiclibrary.TrackFile, rec musicbrainz.Recording, confidence float64, preferredReleaseMBID string, trackNumber, discNumber int, status musiclibrary.MatchStatus) error {
	artistRef := rec.PrimaryArtist()
	if artistRef.ID == "" {
		return fmt.Errorf("recording %s has no artist credit", rec.ID)
	}
	artist, err := s.db.GetOrCreateArtist(artistRef.ID, artistRef.Name, artistRef.SortName)
	if err != nil {
		return fmt.Errorf("get or create artist: %w", err)
	}

	release := rec.BestRelease(preferredReleaseMBID)
	if release.ID == "" {
		return fmt.Errorf("recording %s has no associated release", rec.ID)
	}
	album, err := s.db.GetOrCreateAlbum(artist.ID, release.ID, release.ReleaseGroup.ID, release.Title, release.Date, release.ReleaseGroup.PrimaryType)
	if err != nil {
		return fmt.Errorf("get or create album: %w", err)
	}

	if discNumber == 0 {
		discNumber = 1
	}
	track, err := s.db.GetOrCreateTrack(album.ID, rec.ID, rec.Title, trackNumber, discNumber, int64(rec.Length))
	if err != nil {
		return fmt.Errorf("get or create track: %w", err)
	}

	if err := s.db.SetTrackFileMatch(tf.ID, &track.ID, status, confidence); err != nil {
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
	tf, err := s.db.GetTrackFile(trackFileID)
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

	return s.applyMatch(tf, *rec, 1.0, preferredReleaseMBID, trackNumber, discNumber, musiclibrary.StatusManual)
}

// ClearMatch unlinks trackFileID from whatever track it was matched to,
// moving it back to unmatched — e.g. a manual match the user wants to
// undo. Does not delete the artist/album/track rows themselves, since
// other files may still reference them.
func (s *Scanner) ClearMatch(trackFileID int64) error {
	return s.db.SetTrackFileMatch(trackFileID, nil, musiclibrary.StatusUnmatched, 0)
}

// DeleteTrackFile permanently removes trackFileID: the file itself on
// disk, then its own row — e.g. a wrong/duplicate grab, or junk the user
// doesn't want CantiNode tracking anymore. Not an error if the file is
// already gone from disk (nothing left to remove there either way, same
// tolerance the scanner's own missing-file reconciliation has); any
// other removal error is returned without touching the database row, so
// a permissions problem doesn't silently lose track of a file that's
// still actually there.
func (s *Scanner) DeleteTrackFile(trackFileID int64) error {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return fmt.Errorf("get track file: %w", err)
	}
	if err := os.Remove(tf.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file %s: %w", tf.Path, err)
	}
	return s.db.DeleteTrackFile(trackFileID)
}
