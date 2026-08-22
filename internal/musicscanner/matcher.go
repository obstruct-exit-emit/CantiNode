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

// releaseNeedsArtistCreditCheck reports whether rg is anything other than a
// plain single-artist studio album (PrimaryType "Album", no
// SecondaryTypes) — worth the extra MusicBrainz fetch
// correctArtistCreditForCompilation makes to check the release's own real
// artist-credit. Deliberately broader than just MusicBrainz's own
// "Compilation" SecondaryTypes flag: found live, a real various-artists
// release (a "Cities 97 Sampler" radio-station compilation) whose release
// group MusicBrainz tags SecondaryTypes ["Live"] instead of
// ["Compilation"] — its release-level artist-credit is still correctly
// "Various Artists" (confirmed live against the real API), MusicBrainz's
// own type classification just doesn't say "Compilation." Triggering on
// this broader signal is still correct either way: an ordinary
// single-artist Live album/EP/soundtrack/best-of's own release-level
// credit is just that same one artist, so substituting it is a harmless
// no-op — the cost of a false positive is one extra MusicBrainz request,
// not a wrong result.
func releaseNeedsArtistCreditCheck(rg musicbrainz.ReleaseGroup) bool {
	return rg.PrimaryType != "Album" || len(rg.SecondaryTypes) > 0
}

// releaseCreditCache memoizes correctArtistCreditForCompilation's own
// LookupReleaseWithTracklist fetch, keyed by release ID, across every file
// matchFolder processes in one call — found live, watching a real scan: a
// Various Artists compilation's tracks all resolve to the exact same
// release, so without this an N-track folder paid N identical network
// fetches for data that's the same on every single one (the actual cause
// of tracks visibly leaving Unmatched one by one, seconds apart, instead
// of together). nil is a valid value — every write/read below is
// nil-safe — for a caller with no folder-scoped batch to share a cache
// across (ManualMatch, a one-off single-file match with nothing to
// memoize against).
type releaseCreditCache map[string][]musicbrainz.ArtistCredit

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
// own doc comment on skipping folder-level reasoning). Callers must
// capture the track's own real credit (for display) before calling this —
// it mutates rec.ArtistCredit in place.
//
// Returns a non-nil error only when the correction was actually needed
// (releaseNeedsArtistCreditCheck) and the fetch to perform it failed — a
// real bug found live: a transient MusicBrainz hiccup during this one
// call used to degrade silently to rec's own (wrong, for a compilation)
// credit, producing a full-confidence match under the wrong artist with
// no error anywhere to notice — and worse, once matched, that wrong
// album's mbid permanently "wins" any later correction attempt too (see
// GetOrCreateAlbum's own ON CONFLICT(mbid) recovery). Callers now treat
// this error as an auto-route trigger (see errDirectMatchInconsistent),
// giving the file a real second shot instead of a silent, sticky wrong
// answer. A release that genuinely needs no correction, or whose fetch
// succeeds but returns no artist-credit at all (a real, different
// answer, not a failure), is not an error — see the two returns below.
// ensurePreferredReleasePresent guarantees rec.Releases actually contains
// preferredReleaseMBID's own release, fetching and appending it directly
// when it's missing — mutates rec in place. A real, confirmed MusicBrainz
// API limit: LookupRecording's inc=releases returns at most 25 releases
// per recording (verified live: the dedicated /release?recording=<mbid>
// browse endpoint, which does paginate properly, reported 35 total for a
// real example below), so a recording with a longer release history can
// come back missing the exact release a file's own embedded tag (or a
// human's manual pick via ManualMatch) names — silently, with no error
// anywhere to notice.
//
// That matters because every caller of Recording.BestRelease(preferred)
// trusts an empty search result to mean "no such release, fall back to
// the clean-album heuristic" — which is correct when preferred is simply
// wrong, but wrong when it's simply missing from a truncated list. Found
// live: a Blind Melon "Change" single track, correctly tagged with the
// single's own release MBID, filed under the unrelated "Blind Melon"
// self-titled album instead — the single was the recording's 29th of 35
// known releases, past the 25-item cap, so BestRelease never even
// considered it and fell back to whichever release won the "clean
// studio album" heuristic instead.
//
// Called unconditionally wherever a preferred release MBID is available
// (a file's own embedded tag, or a human's explicit choice) — the common
// case where it's already present costs one cheap local loop; only a
// recording actually past the cap, asked about specifically by its own
// release's MBID, pays for the extra lookup. Best-effort: a lookup
// failure here just leaves BestRelease to its existing heuristic
// fallback, exactly the behavior before this fix existed — never worth
// failing the whole match over.
func (s *Scanner) ensurePreferredReleasePresent(ctx context.Context, rec *musicbrainz.Recording, preferredReleaseMBID string) {
	if preferredReleaseMBID == "" {
		return
	}
	for _, rel := range rec.Releases {
		if rel.ID == preferredReleaseMBID {
			return
		}
	}
	full, err := s.mb.LookupReleaseWithTracklist(ctx, preferredReleaseMBID)
	if err != nil {
		return
	}
	rec.Releases = append(rec.Releases, full.AsRelease())
}

func (s *Scanner) correctArtistCreditForCompilation(ctx context.Context, rec *musicbrainz.Recording, preferredReleaseMBID string, cache releaseCreditCache) error {
	release := rec.BestRelease(preferredReleaseMBID)
	if release.ID == "" {
		return nil
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
		return nil
	}
	if !releaseNeedsArtistCreditCheck(release.ReleaseGroup) {
		return nil
	}
	if cache != nil {
		if credit, ok := cache[release.ID]; ok {
			if len(credit) > 0 {
				rec.ArtistCredit = credit
			}
			return nil
		}
	}
	full, err := s.mb.LookupReleaseWithTracklist(ctx, release.ID)
	if err != nil {
		// Deliberately NOT cached: this release still needs the check, so
		// the next file in the same folder (or a later scan) should get
		// its own real attempt, not a remembered failure.
		return fmt.Errorf("check release %s artist credit: %w", release.ID, err)
	}
	if len(full.ArtistCredit) == 0 {
		// A genuine, successful answer — this release really has no
		// artist-credit to substitute (rare, but definitive, not a
		// failure) — cached like the hit case below.
		if cache != nil {
			cache[release.ID] = []musicbrainz.ArtistCredit{}
		}
		return nil
	}
	rec.ArtistCredit = full.ArtistCredit
	if cache != nil {
		cache[release.ID] = full.ArtistCredit
	}
	return nil
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
func (s *Scanner) matchFileDirect(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags, cache releaseCreditCache) (bool, error) {
	rec, err := s.mb.LookupRecording(ctx, tags.MusicBrainzRecordingID)
	if err != nil {
		return false, fmt.Errorf("lookup recording %s: %w", tags.MusicBrainzRecordingID, err)
	}
	return s.resolveDirectMatch(ctx, tf, tags, rec, cache)
}

// resolveDirectMatch is matchFileDirect's own logic minus the lookup
// itself: the tag-consistency safety checks (embeddedTagsAgree/
// titleAgrees) plus applyMatch, given a recording already in hand — shared
// by matchFileDirect's single-lookup path and matchFolder's batched path
// (folder_match.go), so both apply exactly the same safety checks to a
// recording however it was fetched. cache is releaseCreditCache — see its
// own doc comment; pass the same one folder-processing-wide, or nil for a
// standalone caller.
func (s *Scanner) resolveDirectMatch(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags, rec *musicbrainz.Recording, cache releaseCreditCache) (bool, error) {
	// Must run before embeddedTagsAgree, not just before applyMatch below —
	// a preferred release missing from rec.Releases (see this function's
	// own doc comment) makes embeddedTagsAgree's own release-group check
	// fail too, for the same underlying reason.
	s.ensurePreferredReleasePresent(ctx, rec, tags.MusicBrainzAlbumID)
	if !embeddedTagsAgree(tags, rec) || !titleAgrees(tags, rec) {
		// Decline the fast path rather than confidently matching to the
		// wrong album/song — matchFolder catches this sentinel and gives
		// the file a real shot at whole-folder consensus matching instead.
		return false, errDirectMatchInconsistent
	}
	// Captured before correctArtistCreditForCompilation below, which
	// mutates rec.ArtistCredit in place to the release's own filing credit —
	// trackArtistCredit/trackArtistMBID must keep the recording's real,
	// pre-correction credit (see applyMatch's own params for why).
	trackArtistCredit := joinArtistCredit(rec.ArtistCredit)
	trackArtistMBID := rec.PrimaryArtist().ID
	if err := s.correctArtistCreditForCompilation(ctx, rec, tags.MusicBrainzAlbumID, cache); err != nil {
		// Same auto-route as an embedded-tag inconsistency: don't lock in
		// a possibly-wrong artist credit, give the file a real second shot
		// via whole-folder consensus instead (see errDirectMatchInconsistent
		// and correctArtistCreditForCompilation's own doc comment).
		return false, errDirectMatchInconsistent
	}
	if err := s.applyMatch(tf, *rec, 1.0, tags.MusicBrainzAlbumID, tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, trackArtistCredit, trackArtistMBID); err != nil {
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
// cache is releaseCreditCache — see its own doc comment; pass the same one
// folder-processing-wide, or nil for a standalone caller.
func (s *Scanner) matchFileFuzzy(ctx context.Context, tf *musiclibrary.TrackFile, tags *tagreader.Tags, cache releaseCreditCache) (bool, error) {
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

	// Captured before correctArtistCreditForCompilation below mutates
	// best.ArtistCredit in place — see resolveDirectMatch's identical note.
	trackArtistCredit := joinArtistCredit(best.ArtistCredit)
	trackArtistMBID := best.PrimaryArtist().ID
	// No further fallback beyond this one (matchFileFuzzy is itself the
	// safety valve — see its own doc comment), so unlike resolveDirectMatch
	// a correction failure here just surfaces as a real scan error rather
	// than an auto-route: better a file sits in Unmatched for manual
	// review than silently lock in a possibly-wrong artist credit.
	if err := s.correctArtistCreditForCompilation(ctx, &best, "", cache); err != nil {
		return false, fmt.Errorf("check compilation artist credit: %w", err)
	}
	if err := s.applyMatch(tf, best, confidence, "", tags.TrackNumber, tags.DiscNumber, musiclibrary.StatusMatched, trackArtistCredit, trackArtistMBID); err != nil {
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
// trackArtistCredit/trackArtistMBID are stored on the created track as
// display metadata only — deliberately separate from the artist actually
// resolved below for filing (via rec.PrimaryArtist(), or a tracked series —
// see GetSeriesArtistForReleaseGroup): on a "Various Artists" release (or a
// tracked series, almost always one too), every track must still file
// under the same shared artist/album, but each has its own real performer
// (and that performer's own MusicBrainz ID) worth showing/embedding.
// Callers working from a bare recording lookup (matchFileDirect/
// matchFileFuzzy/ManualMatch) pass the same rec's own credit/ID for both;
// matchEntriesToRelease passes the track's real per-recording credit/ID
// separately, since the Recording it builds has already substituted the
// release's own credit into rec.ArtistCredit for the assignment step.
func (s *Scanner) applyMatch(tf *musiclibrary.TrackFile, rec musicbrainz.Recording, confidence float64, preferredReleaseMBID string, trackNumber, discNumber int, status musiclibrary.MatchStatus, trackArtistCredit, trackArtistMBID string) error {
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
	if trackArtistMBID == artist.MBID {
		trackArtistMBID = ""
	}
	track, err := s.db.GetOrCreateTrack(album.ID, rec.ID, rec.Title, trackNumber, discNumber, int64(rec.Length), trackArtistCredit, trackArtistMBID, rec.Composer())
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
	// A human explicitly chose preferredReleaseMBID (e.g. the version
	// picker) — it must win even if it's past LookupRecording's own
	// truncation cap (see ensurePreferredReleasePresent's own doc
	// comment), not silently lose to BestRelease's heuristic fallback.
	s.ensurePreferredReleasePresent(ctx, rec, preferredReleaseMBID)

	trackNumber, discNumber := 0, 1
	var tags *tagreader.Tags
	if t, err := tagreader.Read(tf.Path); err == nil {
		tags = t
	}
	if tags != nil {
		trackNumber, discNumber = tags.TrackNumber, tags.DiscNumber
	}

	// Captured before correctArtistCreditForCompilation below mutates
	// rec.ArtistCredit in place — see resolveDirectMatch's identical note.
	trackArtistCredit := joinArtistCredit(rec.ArtistCredit)
	trackArtistMBID := rec.PrimaryArtist().ID
	// Best-effort, error deliberately ignored: a human explicitly chose
	// this exact recording, so a transient hiccup on the filing-artist
	// correction shouldn't block the match they asked for — they can
	// always fix the filing manually afterward if it lands wrong.
	_ = s.correctArtistCreditForCompilation(ctx, rec, preferredReleaseMBID, nil)
	return s.applyMatch(tf, *rec, 1.0, preferredReleaseMBID, trackNumber, discNumber, musiclibrary.StatusManual, trackArtistCredit, trackArtistMBID)
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
