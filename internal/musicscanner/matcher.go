package musicscanner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// joinArtistCredit renders a MusicBrainz artist-credit list as a plain
// display string — just the credited names joined together. This
// codebase doesn't track each entry's own joinphrase (almost always
// absent anyway on the common single-artist case), so a genuine
// multi-artist credit renders slightly more bluntly ("Artist A, Artist
// B") than MusicBrainz's own "Artist A feat. Artist B" — good enough for
// "who actually performed this," which is the only thing this string is
// ever used for (see applyMatch's trackArtistCredit param).
func joinArtistCredit(credits []musicbrainz.ArtistCredit) string {
	names := make([]string, 0, len(credits))
	for _, c := range credits {
		if c.Name != "" {
			names = append(names, c.Name)
		}
	}
	return strings.Join(names, ", ")
}

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
	if err := s.applyMatch(tf, *rec, 1.0, tags.MusicBrainzAlbumID, tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, joinArtistCredit(rec.ArtistCredit)); err != nil {
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

	if err := s.applyMatch(tf, best, confidence, "", tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, joinArtistCredit(best.ArtistCredit)); err != nil {
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
//
// trackArtistCredit is stored on the created track as display metadata
// only — deliberately separate from rec.ArtistCredit (used just above for
// artist/album assignment, via rec.PrimaryArtist()): on a "Various
// Artists" release, every track must still file under the same Various
// Artists artist/album, but each has its own real performer worth
// showing. Callers working from a bare recording lookup (matchFileDirect/
// matchFileFuzzy/ManualMatch) pass the same rec's own credit for both;
// matchEntriesToRelease passes the track's real per-recording credit
// separately, since the Recording it builds has already substituted the
// release's own credit into rec.ArtistCredit for the assignment step.
func (s *Scanner) applyMatch(tf *musiclibrary.TrackFile, rec musicbrainz.Recording, confidence float64, preferredReleaseMBID string, trackNumber, discNumber int, status musiclibrary.MatchStatus, trackArtistCredit string) error {
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
	// This album is now demonstrably owned — if it was also sitting in
	// Wanted (added before its files happened to already exist unmatched
	// on disk, or matched here via a path the grab→import pipeline never
	// touches), it must not keep showing up as a second, wanted copy of
	// the same release group alongside the real owned one. Best-effort:
	// a leftover wanted row is a cosmetic duplicate, not worth failing an
	// otherwise-successful match over.
	if err := s.db.ClearWantedAlbumByReleaseGroup(artist.ID, release.ReleaseGroup.ID); err != nil {
		s.logger.Warn("clearing wanted album after match", "artist", artist.Name, "album", album.Title, "error", err)
	}

	if discNumber == 0 {
		discNumber = 1
	}
	// Only stored when it actually differs from the album's own artist —
	// nothing worth showing on an ordinary single-artist album where
	// every track's credit is identical to it.
	if trackArtistCredit == artist.Name {
		trackArtistCredit = ""
	}
	track, err := s.db.GetOrCreateTrack(album.ID, rec.ID, rec.Title, trackNumber, discNumber, int64(rec.Length), trackArtistCredit)
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

	return s.applyMatch(tf, *rec, 1.0, preferredReleaseMBID, trackNumber, discNumber, musiclibrary.StatusManual, joinArtistCredit(rec.ArtistCredit))
}

// ClearMatch unlinks trackFileID from whatever track it was matched to,
// moving it back to unmatched — e.g. a manual match the user wants to
// undo. Does not delete the artist/album/track rows themselves, since
// other files may still reference them — but if this was the album's
// last remaining file, ReapOrphanedAlbum deletes the now-empty album row
// too, so the release group falls back into Missing instead of surviving
// as an invisible, fileless "owned" album (see that method's own doc
// comment for why that dead end matters).
func (s *Scanner) ClearMatch(trackFileID int64) error {
	albumID, err := s.trackFileAlbumID(trackFileID)
	if err != nil {
		return err
	}
	if err := s.db.SetTrackFileMatch(trackFileID, nil, musiclibrary.StatusUnmatched, 0); err != nil {
		return err
	}
	return s.reapIfOrphaned(albumID)
}

// DeleteTrackFile permanently removes trackFileID: the file itself on
// disk, then its own row — e.g. a wrong/duplicate grab, or junk the user
// doesn't want CantiNode tracking anymore. Not an error if the file is
// already gone from disk (nothing left to remove there either way, same
// tolerance the scanner's own missing-file reconciliation has); any
// other removal error is returned without touching the database row, so
// a permissions problem doesn't silently lose track of a file that's
// still actually there. Also reaps the owning album if this was its last
// file — see ClearMatch's own comment for why.
func (s *Scanner) DeleteTrackFile(trackFileID int64) error {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return fmt.Errorf("get track file: %w", err)
	}
	albumID, err := s.trackFileAlbumID(trackFileID)
	if err != nil {
		return err
	}
	if err := os.Remove(tf.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file %s: %w", tf.Path, err)
	}
	if err := s.db.DeleteTrackFile(trackFileID); err != nil {
		return err
	}
	return s.reapIfOrphaned(albumID)
}

// trackFileAlbumID returns the album ID trackFileID's current match
// belongs to, or 0 if it isn't matched to anything — read before a
// clear/delete unlinks it, since ReapOrphanedAlbum needs to know which
// album to check afterward.
func (s *Scanner) trackFileAlbumID(trackFileID int64) (int64, error) {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return 0, fmt.Errorf("get track file: %w", err)
	}
	if tf.TrackID == nil {
		return 0, nil
	}
	track, err := s.db.GetTrack(*tf.TrackID)
	if err != nil {
		return 0, fmt.Errorf("get track: %w", err)
	}
	return track.AlbumID, nil
}

// reapIfOrphaned is a no-op for albumID == 0 (the file being cleared/
// deleted was never matched, so there's no album to check).
func (s *Scanner) reapIfOrphaned(albumID int64) error {
	if albumID == 0 {
		return nil
	}
	return s.db.ReapOrphanedAlbum(albumID)
}
