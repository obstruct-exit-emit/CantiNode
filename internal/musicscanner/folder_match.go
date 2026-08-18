package musicscanner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// folderEntry pairs a not-yet-matched track_file with the tags already
// read for it during ScanRootFolder's walk, so folder-grouped matching
// doesn't need to re-read the file from disk.
type folderEntry struct {
	tf   *musiclibrary.TrackFile
	tags *tagreader.Tags
}

// matchFolder matches every not-yet-matched file from one directory
// (entries — already upserted, tags already read) — CantiNode's
// whole-album matching pass, run once per folder after ScanRootFolder's
// walk finishes. A file with its own embedded MusicBrainz recording ID
// still fast-paths through matchFileDirect, entirely independent of its
// siblings' resolution — unless matchFileDirect finds its embedded ID
// inconsistent with the file's own other tags (errDirectMatchInconsistent,
// matcher.go), in which case it falls into remaining instead, giving
// whole-folder consensus a real shot at it: MusicBrainzAlbumID/Album are
// independent of a bad recording ID and were correct in the real case
// that prompted this (a compilation track whose recording ID pointed at
// an unrelated release).
func (s *Scanner) matchFolder(ctx context.Context, entries []folderEntry, result *ScanResult) {
	var direct []folderEntry
	var remaining []folderEntry
	for _, e := range entries {
		if e.tags.MusicBrainzRecordingID != "" {
			direct = append(direct, e)
			continue
		}
		remaining = append(remaining, e)
	}
	remaining = append(remaining, s.matchDirectEntries(ctx, direct, result)...)
	if len(remaining) == 0 {
		return
	}

	release, confidence, err := s.resolveFolderRelease(ctx, remaining)
	if err != nil {
		s.logger.Warn("folder release resolution failed, falling back to per-file matching",
			"dir", filepath.Dir(remaining[0].tf.Path), "error", err)
	}
	if release == nil {
		// No confident single-release target for this folder — the
		// safety valve. Never a fatal scan error: matchFileFuzzy already
		// tolerates "no match found", and this degrades to exactly the
		// pre-existing per-file algorithm, no worse than before this
		// feature existed.
		for _, e := range remaining {
			matched, ferr := s.matchFileFuzzy(ctx, e.tf, e.tags)
			s.recordFileResult(result, e.tf, matched, ferr)
		}
		return
	}
	s.matchEntriesToRelease(remaining, release, confidence, result)
}

// matchDirectEntries resolves every direct entry's embedded MusicBrainz
// recording ID in as few MusicBrainz requests as possible — one batched
// BatchLookupRecordings call instead of direct's own per-file matchFileDirect
// loop, each of which otherwise pays MusicBrainz's ~1.1s throttle
// individually. Returns the entries that should fall through to
// whole-folder consensus matching instead (matchFolder's own remaining):
// either because resolveDirectMatch found the recording inconsistent with
// the file's own tags (errDirectMatchInconsistent — the same auto-route
// this package has always done), or because the batch call itself failed
// outright, in which case every direct entry falls back to today's
// per-file matchFileDirect loop instead — never worse than before this
// batching existed.
func (s *Scanner) matchDirectEntries(ctx context.Context, direct []folderEntry, result *ScanResult) []folderEntry {
	if len(direct) == 0 {
		return nil
	}

	recs, err := s.batchLookupDirect(ctx, direct)
	if err != nil {
		s.logger.Warn("batch recording lookup failed, falling back to per-file lookups", "error", err)
		return s.matchDirectEntriesPerFile(ctx, direct, result)
	}

	var remaining []folderEntry
	for _, e := range direct {
		rec, ok := recs[e.tags.MusicBrainzRecordingID]
		if !ok {
			// Missing from the batch search results does NOT mean the
			// recording doesn't exist — confirmed live: MusicBrainz's
			// search index can have real gaps relative to its own
			// authoritative per-ID lookup endpoint (a valid recording,
			// fully resolvable via LookupRecording, came back absent from
			// an 18-ID rid:(...) search that correctly returned the other
			// 17). Give it one real shot via the single-lookup path
			// before treating it as gone.
			remaining = append(remaining, s.matchDirectEntriesPerFile(ctx, []folderEntry{e}, result)...)
			continue
		}
		matched, err := s.resolveDirectMatch(ctx, e.tf, e.tags, &rec)
		if errors.Is(err, errDirectMatchInconsistent) {
			remaining = append(remaining, e)
			continue
		}
		s.recordFileResult(result, e.tf, matched, err)
	}
	return remaining
}

// matchDirectEntriesPerFile is matchDirectEntries' own per-file fallback:
// today's matchFileDirect single-lookup path, run for each of entries.
// Used both when the batch call itself fails outright (the whole direct
// slice falls back) and when one specific ID comes back missing from an
// otherwise-successful batch (see matchDirectEntries' own doc comment on
// why a batch miss still deserves one authoritative single-lookup shot).
// Returns the entries that should fall through to whole-folder consensus
// (errDirectMatchInconsistent) — matchDirectEntries' own callers pass that
// straight back to matchFolder's own remaining.
func (s *Scanner) matchDirectEntriesPerFile(ctx context.Context, entries []folderEntry, result *ScanResult) []folderEntry {
	var remaining []folderEntry
	for _, e := range entries {
		matched, err := s.matchFileDirect(ctx, e.tf, e.tags)
		if errors.Is(err, errDirectMatchInconsistent) {
			remaining = append(remaining, e)
			continue
		}
		s.recordFileResult(result, e.tf, matched, err)
	}
	return remaining
}

// batchLookupDirect collects direct's distinct embedded recording IDs and
// resolves them in one call — split out from matchDirectEntries mainly so
// the "collect IDs, call once" step is easy to unit test in isolation.
func (s *Scanner) batchLookupDirect(ctx context.Context, direct []folderEntry) (map[string]musicbrainz.Recording, error) {
	seen := make(map[string]bool, len(direct))
	ids := make([]string, 0, len(direct))
	for _, e := range direct {
		id := e.tags.MusicBrainzRecordingID
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return s.mb.BatchLookupRecordings(ctx, ids)
}

// resolveFolderRelease decides the single MusicBrainz release remaining's
// files should all converge on. Returns nil (with a nil error) whenever
// the signal is too weak to trust — that is not an error, it's the
// caller's cue to fall back to independent per-file matching.
func (s *Scanner) resolveFolderRelease(ctx context.Context, remaining []folderEntry) (*musicbrainz.ReleaseWithTracklist, float64, error) {
	if mbid := embeddedReleaseMBID(remaining); mbid != "" {
		release, err := s.mb.LookupReleaseWithTracklist(ctx, mbid)
		if err != nil {
			// A stale/typo'd embedded MBID (404, etc.) — same graceful
			// degrade as any other unresolved folder, not a hard error.
			return nil, 0, nil
		}
		return release, 1.0, nil
	}

	if release, confidence, ok := s.resolveExpectedRelease(ctx, remaining); ok {
		return release, confidence, nil
	}

	if len(remaining) == 1 {
		// Nothing to disambiguate with a single file — a release search
		// only earns its cost when there's more than one sibling to
		// converge onto a shared answer (the actual bug this rework
		// fixes). A genuinely standalone file is just as well served by
		// the original per-file fuzzy search, at a third of the
		// MusicBrainz round trips.
		return nil, 0, nil
	}

	artist, album, ok := folderTagConsensus(remaining)
	if !ok {
		return nil, 0, nil // inconsistent tags — not really "one album"
	}

	candidates, err := s.mb.SearchReleases(ctx, artist, album)
	if err != nil {
		return nil, 0, fmt.Errorf("search releases: %w", err)
	}
	best := pickBestReleaseCandidate(candidates, len(remaining), s.getMinMatchConfidence())
	if best == nil {
		return nil, 0, nil
	}
	release, err := s.mb.LookupReleaseWithTracklist(ctx, best.ID)
	if err != nil {
		return nil, 0, nil
	}
	return release, float64(best.Score) / 100.0, nil
}

// resolveExpectedRelease is resolveFolderRelease's grab-provenance fast
// path: when every file in remaining was stamped with the same
// ExpectedReleaseGroupMBID by internal/importer (see that field's own doc
// comment on musiclibrary.TrackFile), the release GROUP is already
// certain — no reason to spend a MusicBrainz search re-deriving it blind
// from tags the way a manually-added file has to. This only ever skips
// the search step, never per-track verification: the release it returns
// still flows through matchEntriesToRelease's own title/position matching
// exactly like any other resolved release, which is what actually catches
// a wrong individual file.
//
// ok is false — falling through to the normal search-based path above —
// whenever there's a real reason not to trust the shortcut: mixed or
// absent expectations across the folder, no cached version yet for the
// expected release group, no version whose track count is even roughly
// plausible for this folder's file count, or — the actual safety check —
// the folder's own tags positively naming an album that doesn't look like
// any cached version's title. A folder with no album tag to check at all
// has nothing to contradict the expectation, so the shortcut proceeds;
// that's strictly better than today, where an untagged file has nothing
// to fuzzy-search against either.
func (s *Scanner) resolveExpectedRelease(ctx context.Context, remaining []folderEntry) (*musicbrainz.ReleaseWithTracklist, float64, bool) {
	expected := ""
	for i, e := range remaining {
		mbid := e.tf.ExpectedReleaseGroupMBID
		if mbid == "" {
			return nil, 0, false
		}
		if i == 0 {
			expected = mbid
		} else if expected != mbid {
			return nil, 0, false
		}
	}

	versions, err := s.db.ListReleaseGroupVersions(expected)
	if err != nil || len(versions) == 0 {
		return nil, 0, false
	}

	// folderTagConsensus's ok=false conflates two very different cases: no
	// album tag present anywhere (nothing to check — fine to proceed, see
	// this function's own doc comment) and album tags that actively
	// DISAGREE with each other (a real red flag — a folder whose own files
	// can't even agree what album they are is exactly the kind of
	// internally-inconsistent "this grab might not be what was expected"
	// signal the safety gate exists for, and must never be silently
	// treated the same as "nothing to check"). albumTagsDisagree checks
	// that specific case on its own, ahead of the normal consensus check.
	if albumTagsDisagree(remaining) {
		return nil, 0, false
	}
	const albumTitleMatchThreshold = 0.5 // more lenient than slotTrack's own 0.6 — album titles carry more legitimate edition-to-edition variance ("(Remastered)", "(Deluxe)") than a single track title does
	if _, album, ok := folderTagConsensus(remaining); ok {
		matchesAny := false
		for _, v := range versions {
			if titleSimilarity(album, v.Title) >= albumTitleMatchThreshold {
				matchesAny = true
				break
			}
		}
		if !matchesAny {
			return nil, 0, false
		}
	}

	best := pickBestVersionByFileCount(versions, len(remaining))
	if best == nil {
		return nil, 0, false
	}
	release, err := s.mb.LookupReleaseWithTracklist(ctx, best.ReleaseMBID)
	if err != nil {
		return nil, 0, false
	}
	// High, but deliberately short of embeddedReleaseMBID's 1.0 — that
	// signal names an exact release directly from the file's own tags;
	// this one is one step more inferred (a release group plus a
	// file-count-based edition guess), even though both skip the search.
	return release, 0.95, true
}

// embeddedReleaseMBID returns the one release MBID entries agree on, if
// any file carries one — the strongest possible signal. Conflicting
// non-empty values (two files naming two different releases)
// intentionally return "" rather than picking one arbitrarily, falling
// through to the tag-consensus search path instead.
func embeddedReleaseMBID(entries []folderEntry) string {
	found := ""
	for _, e := range entries {
		mbid := e.tags.MusicBrainzAlbumID
		if mbid == "" {
			continue
		}
		if found != "" && found != mbid {
			return ""
		}
		found = mbid
	}
	return found
}

// albumTagsDisagree reports whether two or more entries' own non-empty
// Album tags name different albums. Distinct from folderTagConsensus's
// ok=false, which also fires when there's simply nothing to check (no
// Album tag anywhere) — this only fires on an actual internal
// contradiction, the specific red flag resolveExpectedRelease's safety
// gate needs to catch ahead of the normal consensus check.
func albumTagsDisagree(entries []folderEntry) bool {
	album := ""
	for _, e := range entries {
		a := strings.TrimSpace(e.tags.Album)
		if a == "" {
			continue
		}
		if album == "" {
			album = a
		} else if !strings.EqualFold(album, a) {
			return true
		}
	}
	return false
}

// folderTagConsensus derives the one Artist+Album a folder's files agree
// they belong to. Deliberately strict, not a majority vote: every file
// with a non-empty Album tag must name the exact same album
// (case-insensitive), and every file with a non-empty AlbumArtist/Artist
// tag must name the exact same artist. A genuinely mixed folder (e.g.
// loose singles dropped directly in a root folder) fails this check
// outright rather than silently picking a plurality and mismatching
// whichever files disagree with it — see matchFolder's fallback.
func folderTagConsensus(entries []folderEntry) (artist, album string, ok bool) {
	albumAgrees := true
	artistAgrees := true
	albumArtistSeen := false // true once any file supplies a real AlbumArtist override
	distinctArtists := map[string]bool{}

	for _, e := range entries {
		if a := strings.TrimSpace(e.tags.Album); a != "" {
			switch {
			case album == "":
				album = a
			case !strings.EqualFold(album, a):
				albumAgrees = false
			}
		}
		aa := strings.TrimSpace(e.tags.AlbumArtist)
		if aa != "" {
			albumArtistSeen = true
		}
		ar := aa
		if ar == "" {
			ar = strings.TrimSpace(e.tags.Artist)
		}
		if ar != "" {
			distinctArtists[strings.ToLower(ar)] = true
			switch {
			case artist == "":
				artist = ar
			case !strings.EqualFold(artist, ar):
				artistAgrees = false
			}
		}
	}
	if !albumAgrees || album == "" {
		return "", "", false // no shared album to search a release by
	}
	if artistAgrees {
		// Already covers a properly VA-tagged folder (AlbumArtist =
		// "Various Artists" set consistently on every file) — that's
		// just an ordinary agreeing artist as far as this function is
		// concerned, no special-casing needed.
		return artist, album, true
	}
	// Artists disagree. A genuine compilation signal only when nothing
	// ever supplied an explicit AlbumArtist override and at least three
	// really-different per-track artists are involved — that's a folder
	// that agrees on the album but was simply never given a shared
	// AlbumArtist tag, the common case for a less carefully tagged
	// compilation. Requiring three (not two) guards against a single
	// mistagged or "feat."-credited track turning an ordinary single-artist
	// album into a false compilation match. An AlbumArtist that itself
	// disagrees across files, or a lone stray mismatch, stays a hard
	// failure: more likely broken tagging than a real compilation, not
	// worth guessing at.
	if !albumArtistSeen && len(distinctArtists) >= 3 {
		return "Various Artists", album, true
	}
	return "", "", false
}

// pickBestReleaseCandidate scores each SearchReleases candidate on
// MusicBrainz's own relevance (0-100) combined with how close its own
// track count is to fileCount — the strongest disambiguator per-recording
// search never had: three different pressings of the same release-group
// usually score similarly on relevance alone, but rarely share a folder's
// exact file count. minConfidence still gates on relevance alone (never
// rescued by a lucky track-count match), so it keeps its existing meaning
// as a floor.
func pickBestReleaseCandidate(candidates []musicbrainz.ReleaseSearchResult, fileCount int, minConfidence float64) *musicbrainz.ReleaseSearchResult {
	var best *musicbrainz.ReleaseSearchResult
	var bestScore float64
	for i := range candidates {
		c := &candidates[i]
		relevance := float64(c.Score) / 100.0
		if relevance < minConfidence {
			continue
		}
		combined := relevance - trackCountPenalty(c.TrackCount, fileCount)
		if best == nil || combined > bestScore {
			best, bestScore = c, combined
		}
	}
	return best
}

// trackCountPenalty is a same-scale penalty against relevance for how far
// candidateTracks is from fileCount (0 = exact match, growing with the
// relative gap). Missing track-count data gets a small flat penalty
// rather than a free pass, so a well-formed match still wins ties against
// it. Deliberately simple — a tunable starting point, not a claim of
// precision.
func trackCountPenalty(candidateTracks, fileCount int) float64 {
	if candidateTracks <= 0 || fileCount <= 0 {
		return 0.05
	}
	diff := candidateTracks - fileCount
	if diff < 0 {
		diff = -diff
	}
	return float64(diff) / float64(fileCount) * 0.5
}

// pickBestVersionByFileCount scores each already-cached version of a
// release group by how close its own track count is to fileCount —
// ties broken toward the representative version. Go port of the
// frontend's identical helper (web/src/views/UnmatchedFilesView.tsx),
// used here by resolveFolderRelease's grab-provenance fast path to pick
// which specific edition of an already-certain release group a folder's
// files most likely are, the same way the manual matching UI already
// lets a human do for themselves. Returns nil if no version has a usable
// (positive) track count at all.
func pickBestVersionByFileCount(versions []musiclibrary.ReleaseGroupVersion, fileCount int) *musiclibrary.ReleaseGroupVersion {
	var best *musiclibrary.ReleaseGroupVersion
	bestDiff := -1
	for i := range versions {
		v := &versions[i]
		if v.TrackCount <= 0 {
			continue
		}
		diff := v.TrackCount - fileCount
		if diff < 0 {
			diff = -diff
		}
		if best == nil || diff < bestDiff || (diff == bestDiff && v.IsRepresentative && !best.IsRepresentative) {
			best, bestDiff = v, diff
		}
	}
	return best
}

// flatTrack is one release track flattened out of
// ReleaseWithTracklist.Media, with its medium's Position resolved to a
// disc number (defaulting to 1, same convention applyMatch already uses
// for a file's own missing disc number).
type flatTrack struct {
	disc int
	musicbrainz.ReleaseTrack
}

func flattenTracks(release *musicbrainz.ReleaseWithTracklist) []flatTrack {
	var out []flatTrack
	for _, medium := range release.Media {
		disc := medium.Position
		if disc == 0 {
			disc = 1
		}
		for _, t := range medium.Tracks {
			out = append(out, flatTrack{disc: disc, ReleaseTrack: t})
		}
	}
	return out
}

// matchEntriesToRelease slots each of entries into a specific track
// within release (already fetched with its full tracklist), applying
// each successful slot through the existing applyMatch — reusing all of
// its artist/album/track upsert logic unchanged. A file that can't be
// confidently slotted is left unmatched rather than guessed, and does not
// block its siblings.
func (s *Scanner) matchEntriesToRelease(entries []folderEntry, release *musicbrainz.ReleaseWithTracklist, confidence float64, result *ScanResult) {
	tracks := flattenTracks(release)
	used := make(map[int]bool, len(tracks)) // indices into tracks already claimed this pass, so two files can't both claim the same slot

	for _, e := range entries {
		idx, ft, ok := slotTrack(e.tags, tracks, used)
		if !ok {
			continue // left unmatched for manual review
		}
		used[idx] = true
		rec := recordingForReleaseTrack(ft, release)
		// ft.Recording.ArtistCredit — the track's own real per-recording
		// credit, before recordingForReleaseTrack substitutes the
		// release's own credit into rec for artist/album assignment — is
		// what actually gets displayed; see applyMatch's trackArtistCredit
		// param for why the two must stay distinct.
		err := s.applyMatch(e.tf, rec, confidence, release.ID, ft.Position, ft.disc, musiclibrary.StatusMatched, joinArtistCredit(ft.Recording.ArtistCredit))
		s.recordFileResult(result, e.tf, err == nil, err)
	}
}

// recordingForReleaseTrack shapes ft into a musicbrainz.Recording so it
// can be passed through applyMatch's existing, unmodified plumbing: since
// applyMatch resolves its target release via
// Recording.BestRelease(preferredReleaseMBID), it only needs
// Recording.Releases to contain release itself. ft.Recording already
// carries the real recording ID/Length from MusicBrainz. ArtistCredit is
// deliberately overwritten with the release's own credit rather than
// ft.Recording's real per-track one (confirmed live to actually be
// present and correct, e.g. a "Various Artists" compilation's own tracks
// each carrying their real individual performer — see
// matchEntriesToRelease's own use of it for display) — applyMatch's
// artist/album assignment must stay keyed off the release's credit
// (Various Artists) so every track of a compilation files under the same
// artist/album, not scattered across whichever individual performers its
// tracks happen to credit.
func recordingForReleaseTrack(ft flatTrack, release *musicbrainz.ReleaseWithTracklist) musicbrainz.Recording {
	title := ft.Recording.Title
	if title == "" {
		title = ft.Title
	}
	return musicbrainz.Recording{
		ID:           ft.Recording.ID,
		Title:        title,
		Length:       ft.Recording.Length,
		ArtistCredit: release.ArtistCredit,
		Releases:     []musicbrainz.Release{release.AsRelease()},
	}
}

// slotTrack picks which of tracks (not yet claimed by an earlier file
// this pass — see used) a local file's tags most likely correspond to:
// first an in-range disc+track number match (cheap, reliable whenever a
// ripper/tagger already numbered files correctly), falling back to the
// file's own title scored against each remaining candidate's title — no
// further network call, since the full tracklist is already in hand.
// Returns ok=false if neither signal produces a confident, unclaimed
// candidate.
func slotTrack(tags *tagreader.Tags, tracks []flatTrack, used map[int]bool) (int, flatTrack, bool) {
	if tags.TrackNumber > 0 {
		disc := tags.DiscNumber
		if disc == 0 {
			disc = 1
		}
		for i, t := range tracks {
			if !used[i] && t.disc == disc && t.Position == tags.TrackNumber {
				return i, t, true
			}
		}
	}

	if tags.Title == "" {
		return 0, flatTrack{}, false
	}
	const titleMatchThreshold = 0.6 // below strict equality: real tags carry harmless punctuation/case noise ("Layla (Acoustic)" vs "Layla - Acoustic")
	bestIdx, bestScore := -1, 0.0
	for i, t := range tracks {
		if used[i] {
			continue
		}
		if score := titleSimilarity(tags.Title, t.Title); score > bestScore {
			bestIdx, bestScore = i, score
		}
	}
	if bestIdx < 0 || bestScore < titleMatchThreshold {
		return 0, flatTrack{}, false
	}
	return bestIdx, tracks[bestIdx], true
}

// discFolderPattern matches a folder name that's clearly one disc of a
// multi-disc release: "CD1", "CD 2", "Disc1", "Disc 03", "D1", "disk2".
// Anchored (^...$) so it never matches a folder that merely contains one
// of these words as a substring of something else (e.g. "Disco Inferno").
var discFolderPattern = regexp.MustCompile(`(?i)^(?:cd|disc|disk|d)[\s_.-]*0*([0-9]+)$`)

// discSuffixPattern matches a trailing disc-number qualifier commonly
// tagged onto a per-disc file's own Album field — "Moonglow CD 1",
// "Moonglow (Disc 2)", "Moonglow - CD2" — stripped before comparing two
// discs' Album tags for multi-disc-merge agreement (see
// groupMultiDiscFolders): real-world rips very often tag each disc's
// Album distinctly (a different qualifier per disc) even though it's
// genuinely one release, so comparing the raw tag verbatim would reject
// merging two discs of the very album this function exists to merge.
var discSuffixPattern = regexp.MustCompile(`(?i)[\s([-]+(?:cd|disc|disk|d)[\s._-]*0*[0-9]+[)\]]?\s*$`)

// discPrefixPattern is discSuffixPattern's mirror for the other common
// per-disc tagging convention — a LEADING qualifier ("CD1 - Moonglow",
// "Disc 2: Moonglow") instead of a trailing one. Real-world rips use
// either convention about as often, so stripping only trailing qualifiers
// would still fail to merge two discs tagged with the prefix style.
var discPrefixPattern = regexp.MustCompile(`(?i)^(?:cd|disc|disk|d)[\s._-]*0*[0-9]+[\s:.-]+`)

// stripDiscSuffix removes a disc-number qualifier from album, wherever it
// appears — trailing ("Moonglow CD 1" -> "Moonglow") or leading ("CD1 -
// Moonglow" -> "Moonglow"); "Wish You Were Here" is unchanged either way.
func stripDiscSuffix(album string) string {
	album = discPrefixPattern.ReplaceAllString(album, "")
	album = discSuffixPattern.ReplaceAllString(album, "")
	return strings.TrimSpace(album)
}

// groupMultiDiscFolders re-groups per-directory folder groups (as built by
// ScanRootFolder/ScanAlbumFolder's own filepath.Dir keying), merging
// sibling directories that are disc-subfolders of the same multi-disc
// album into one logical group under their shared parent — a
// ```
// Album/
//
//	CD1/01 - Track.flac
//	CD2/01 - Track.flac
//
// ```
// layout otherwise produces two entirely independent folder groups (see
// ScanRootFolder's doc comment on filepath.Dir keying), each separately
// searched/matched against MusicBrainz — twice the cost, and no guarantee
// both converge on the same release. Each merged file's disc number is
// inferred from its folder name whenever the file's own tags don't already
// carry one (tags always win when present).
//
// Deliberately conservative about what counts as "the same album": a
// bundle of *different* albums (a discography/box-set dump, each in its
// own subfolder — some of which might themselves be internally multi-disc)
// must not be merged into one release search, since that would search for
// one release using tags naming several different albums. Only merges
// disc-pattern subfolders whose own folderTagConsensus agrees on the same
// Artist+Album; anything that disagrees (or has no internal consensus of
// its own) is left as separate groups, exactly as before this function
// existed — same degrade-to-per-folder behavior a genuine discography pack
// already gets.
func groupMultiDiscFolders(groups map[string][]folderEntry) map[string][]folderEntry {
	byParent := map[string][]string{}
	for dir := range groups {
		if discFolderPattern.MatchString(filepath.Base(dir)) {
			parent := filepath.Dir(dir)
			byParent[parent] = append(byParent[parent], dir)
		}
	}

	out := make(map[string][]folderEntry, len(groups))
	merged := map[string]bool{}
	for parent, subdirs := range byParent {
		if len(subdirs) < 2 {
			continue // a single "CD1"-named folder alone isn't worth merging
		}
		sort.Strings(subdirs)

		var refArtist, refAlbum string
		agree := true
		for i, dir := range subdirs {
			artist, album, ok := folderTagConsensus(groups[dir])
			if !ok {
				agree = false
				break
			}
			album = stripDiscSuffix(album)
			if i == 0 {
				refArtist, refAlbum = artist, album
				continue
			}
			// Artist agreement: both sides having no artist tag at all is
			// still "no signal either way" (album match alone decides, as
			// before) — but one side present and the other blank is now a
			// mismatch, not a free pass. Previously the check only ever
			// fired when BOTH sides were non-empty and differed, so a
			// disc with no artist tag at all could merge into a
			// completely different album/artist that happened to share a
			// (post-suffix-strip) title — exactly the kind of mixed
			// discography/box-set bundle this function exists to keep
			// apart.
			artistsAgree := (refArtist == "" && artist == "") ||
				(refArtist != "" && artist != "" && strings.EqualFold(refArtist, artist))
			if !strings.EqualFold(refAlbum, album) || !artistsAgree {
				agree = false
				break
			}
		}
		if !agree {
			continue
		}

		var mergedEntries []folderEntry
		for _, dir := range subdirs {
			disc := inferDiscNumber(filepath.Base(dir))
			for _, e := range groups[dir] {
				if disc > 0 && e.tags.DiscNumber == 0 {
					tagsCopy := *e.tags
					tagsCopy.DiscNumber = disc
					e = folderEntry{tf: e.tf, tags: &tagsCopy}
				}
				mergedEntries = append(mergedEntries, e)
			}
			merged[dir] = true
		}
		// The parent directory itself may also hold loose files that sit
		// directly alongside the CD1/CD2 subfolders (e.g. a stray bonus
		// track). Fold them into the merged entry and mark the parent as
		// accounted for, otherwise the loop below — which copies through
		// every group not already merged — would overwrite out[parent]
		// with just those loose files, silently dropping every file the
		// merge above just combined.
		if loose, ok := groups[parent]; ok {
			mergedEntries = append(mergedEntries, loose...)
		}
		merged[parent] = true
		out[parent] = mergedEntries
	}
	for dir, entries := range groups {
		if !merged[dir] {
			out[dir] = entries
		}
	}
	return out
}

// inferDiscNumber extracts the disc number from a disc-pattern folder name
// ("CD2" -> 2), or 0 if base doesn't match discFolderPattern at all.
func inferDiscNumber(base string) int {
	m := discFolderPattern.FindStringSubmatch(base)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}
