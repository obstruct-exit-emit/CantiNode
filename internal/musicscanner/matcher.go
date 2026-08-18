package musicscanner

import (
	"context"
	"errors"
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

// isCompilationRelease reports whether rg is flagged as anything other
// than a plain single-artist studio album — MusicBrainz's own
// SecondaryTypes signal, which covers both an ordinary artist's own "Best
// Of" and a genuine Various Artists release. correctArtistCreditForCompilation
// triggering on the broader signal is still correct either way: an
// ordinary compilation's release-level credit is just that same one
// artist, so substituting it is a harmless no-op.
func isCompilationRelease(rg musicbrainz.ReleaseGroup) bool {
	for _, t := range rg.SecondaryTypes {
		if strings.EqualFold(t, "Compilation") {
			return true
		}
	}
	return false
}

// correctArtistCreditForCompilation mutates rec's own ArtistCredit to its
// resolved release's ArtistCredit, when that release is a compilation —
// the fix for a real bug reported live: a per-file match (matchFileDirect's
// embedded-MBID fast path, or matchFileFuzzy's standalone fallback) filed
// every track under its own recording-level artist-credit — the real
// per-track PERFORMER, not necessarily who the RELEASE itself is credited
// to. The two are identical for an ordinary single-artist album (harmless
// no-op here) but diverge exactly on a Various Artists compilation, where
// every recording still credits its own real performer — filing each
// track under that instead of the release's shared "Various Artists"
// credit is how one compilation folder ended up scattered across a dozen
// different artist folders instead of one. matchEntriesToRelease
// (folder_match.go) already does the equivalent substitution for its own
// whole-folder path (see recordingForReleaseTrack); this is the same fix
// for the per-file paths, which bypass that entirely (matchFileDirect's
// own doc comment on skipping folder-level reasoning). Best-effort: a
// failed lookup just leaves rec as-is, same as before this existed —
// never worth failing an otherwise-successful match over a filing nicety.
// Callers must capture the track's own real credit (for display) before
// calling this — it mutates rec.ArtistCredit in place.
func (s *Scanner) correctArtistCreditForCompilation(ctx context.Context, rec *musicbrainz.Recording, preferredReleaseMBID string) {
	release := rec.BestRelease(preferredReleaseMBID)
	if release.ID == "" {
		return
	}
	// A release group tracked as part of a monitored series gets its
	// filing artist resolved locally by applyMatch regardless of what this
	// function does to rec.ArtistCredit (see
	// GetSeriesArtistForReleaseGroup) — paying for a network
	// LookupReleaseWithTracklist call here whose result would just get
	// overridden anyway is pure waste, and a series entry is almost always
	// also a compilation release, so this early exit matters in practice
	// for every series-tracked match.
	if _, isSeries, err := s.db.GetSeriesArtistForReleaseGroup(release.ReleaseGroup.ID); err == nil && isSeries {
		return
	}
	if !isCompilationRelease(release.ReleaseGroup) {
		return
	}
	full, err := s.mb.LookupReleaseWithTracklist(ctx, release.ID)
	if err != nil || len(full.ArtistCredit) == 0 {
		return
	}
	rec.ArtistCredit = full.ArtistCredit
}

// embeddedTagsAgree reports whether tags' own MusicBrainzReleaseGroupID
// (when present) names the release group of at least one of rec's own
// known releases — the sanity check matchFileDirect runs before trusting
// an embedded recording ID absolutely. An empty tag means there's nothing
// to check (nothing else the file asserts to contradict it); a non-empty
// tag that matches none of rec's releases is the red flag: the file's own
// tags disagree with each other about what this recording actually is.
//
// Found live: a real compilation track (Birdy's "Skinny Love" cover on a
// Cities 97 Sampler volume) whose MusicBrainzReleaseGroupID tag correctly
// named the compilation, but whose MusicBrainzRecordingID pointed at a
// recording MusicBrainz only links to Birdy's own single/album — a real,
// fairly common MusicBrainz data-duplication pattern (a compilation often
// gets its own, disconnected recording entry rather than reusing the
// "official" one). Trusting the recording ID there silently matched the
// file to the wrong album at full confidence.
func embeddedTagsAgree(tags *tagreader.Tags, rec *musicbrainz.Recording) bool {
	if tags.MusicBrainzReleaseGroupID == "" {
		return true
	}
	for _, rel := range rec.Releases {
		if rel.ReleaseGroup.ID == tags.MusicBrainzReleaseGroupID {
			return true
		}
	}
	return false
}

// titleAgrees reports whether tags' own Title (when present) is plausibly
// the same song as rec's — catches a stale-but-internally-consistent
// embedded recording ID: one with no release-group tag to contradict it
// (or none present at all) but that's simply the wrong recording. Same
// threshold and tolerance-for-tag-noise reasoning as slotTrack's own
// title fallback (folder_match.go) — kept as its own literal constant
// here since the two independently document their own reasoning, not
// because the value differs.
func titleAgrees(tags *tagreader.Tags, rec *musicbrainz.Recording) bool {
	if tags.Title == "" || rec.Title == "" {
		return true
	}
	const directMatchTitleThreshold = 0.6
	return titleSimilarity(tags.Title, rec.Title) >= directMatchTitleThreshold
}

// errDirectMatchInconsistent is matchFileDirect's own sentinel for "the
// embedded recording ID doesn't check out" (embeddedTagsAgree or
// titleAgrees failed) — not a real error. matchFolder (folder_match.go)
// catches this specifically via errors.Is and gives the file a shot at
// whole-folder consensus matching instead of recording a scan error or
// leaving it unmatched outright. Deliberately narrow: a genuine lookup
// failure (network, 404, stale MBID) still returns its own real error and
// keeps today's behavior (recorded in ScanResult.Errors) — only a
// positively-detected internal inconsistency reroutes.
var errDirectMatchInconsistent = errors.New("embedded recording ID is inconsistent with the file's own tags")

// matchFileDirect looks tf up directly by the MusicBrainz recording ID
// already embedded in its own tags — confidence 1.0. Bypasses all
// folder-level reasoning (see folder_match.go): the file's own tags are
// already as authoritative as MusicBrainz gets, so long as they actually
// agree with each other (see embeddedTagsAgree/titleAgrees). Precondition:
// tags.MusicBrainzRecordingID != "". The single-lookup fallback for a
// caller with no batch of its own to fetch alongside this one (matchFolder's
// batched path calls resolveDirectMatch directly instead, on a rec already
// fetched via BatchLookupRecordings — see its own doc comment).
func (s *Scanner) matchFileDirect(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags) (bool, error) {
	rec, err := s.mb.LookupRecording(ctx, tags.MusicBrainzRecordingID)
	if err != nil {
		return false, fmt.Errorf("lookup recording %s: %w", tags.MusicBrainzRecordingID, err)
	}
	return s.resolveDirectMatch(ctx, tf, tags, rec)
}

// resolveDirectMatch is matchFileDirect's own logic minus the lookup
// itself: the tag-consistency safety checks (embeddedTagsAgree/
// titleAgrees) plus applyMatch, given a recording already in hand — shared
// by matchFileDirect's single-lookup path and matchFolder's batched path
// (folder_match.go), so both apply exactly the same safety checks to a
// recording however it was fetched.
func (s *Scanner) resolveDirectMatch(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags, rec *musicbrainz.Recording) (bool, error) {
	if !embeddedTagsAgree(tags, rec) || !titleAgrees(tags, rec) {
		// Decline the fast path rather than confidently matching to the
		// wrong album/song — matchFolder catches this sentinel and gives
		// the file a real shot at whole-folder consensus matching instead.
		return false, errDirectMatchInconsistent
	}
	trackArtistCredit := joinArtistCredit(rec.ArtistCredit)
	s.correctArtistCreditForCompilation(ctx, rec, tags.MusicBrainzAlbumID)
	if err := s.applyMatch(tf, *rec, 1.0, tags.MusicBrainzAlbumID, tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, trackArtistCredit); err != nil {
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

	trackArtistCredit := joinArtistCredit(best.ArtistCredit)
	s.correctArtistCreditForCompilation(ctx, &best, "")
	if err := s.applyMatch(tf, best, confidence, "", tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, trackArtistCredit); err != nil {
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
// only — deliberately separate from the artist actually resolved below for
// filing (via rec.PrimaryArtist(), or a tracked series — see
// GetSeriesArtistForReleaseGroup): on a "Various Artists" release (or a
// tracked series, almost always one too), every track must still file
// under the same shared artist/album, but each has its own real performer
// worth showing. Callers working from a bare recording lookup
// (matchFileDirect/matchFileFuzzy/ManualMatch) pass the same rec's own
// credit for both; matchEntriesToRelease passes the track's real
// per-recording credit separately, since the Recording it builds has
// already substituted the release's own credit into rec.ArtistCredit for
// the assignment step.
func (s *Scanner) applyMatch(tf *musiclibrary.TrackFile, rec musicbrainz.Recording, confidence float64, preferredReleaseMBID string, trackNumber, discNumber int, status musiclibrary.MatchStatus, trackArtistCredit string) error {
	release := rec.BestRelease(preferredReleaseMBID)
	if release.ID == "" {
		return fmt.Errorf("recording %s has no associated release", rec.ID)
	}

	// A release group already tracked as part of a monitored series (see
	// musiclibrary.Artist.Kind) files under that series artist instead of
	// the recording/release's own real MusicBrainz artist-credit — almost
	// always "Various Artists" for a compilation series, which is far less
	// useful for browsing than grouping every entry under the series
	// itself. Cheap, local, no network: the check this whole feature
	// depends on to actually work end to end.
	seriesArtist, isSeries, err := s.db.GetSeriesArtistForReleaseGroup(release.ReleaseGroup.ID)
	if err != nil {
		return fmt.Errorf("check series membership: %w", err)
	}

	var artist *musiclibrary.Artist
	if isSeries {
		artist = seriesArtist
	} else {
		artistRef := rec.PrimaryArtist()
		if artistRef.ID == "" {
			return fmt.Errorf("recording %s has no artist credit", rec.ID)
		}
		artist, err = s.db.GetOrCreateArtist(artistRef.ID, artistRef.Name, artistRef.SortName)
		if err != nil {
			return fmt.Errorf("get or create artist: %w", err)
		}
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

	trackArtistCredit := joinArtistCredit(rec.ArtistCredit)
	s.correctArtistCreditForCompilation(ctx, rec, preferredReleaseMBID)
	return s.applyMatch(tf, *rec, 1.0, preferredReleaseMBID, trackNumber, discNumber, musiclibrary.StatusManual, trackArtistCredit)
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
