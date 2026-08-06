package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// folderEntry pairs a not-yet-matched track_file with the tags already
// read for it during ScanRootFolder's walk, so folder-grouped matching
// doesn't need to re-read the file from disk.
type folderEntry struct {
	tf   *database.TrackFile
	tags *tagreader.Tags
}

// matchFolder matches every not-yet-matched file from one directory
// (entries — already upserted, tags already read) — CantiNode's
// whole-album matching pass, run once per folder after ScanRootFolder's
// walk finishes. A file with its own embedded MusicBrainz recording ID
// still fast-paths through matchFileDirect, entirely independent of its
// siblings' resolution.
func (s *Scanner) matchFolder(ctx context.Context, entries []folderEntry, result *ScanResult) {
	var remaining []folderEntry
	for _, e := range entries {
		if e.tags.MusicBrainzRecordingID != "" {
			matched, err := s.matchFileDirect(ctx, e.tf, e.tags)
			s.recordFileResult(ctx, result, e.tf, matched, err)
			continue
		}
		remaining = append(remaining, e)
	}
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
			s.recordFileResult(ctx, result, e.tf, matched, ferr)
		}
		return
	}
	s.matchEntriesToRelease(ctx, remaining, release, confidence, result)
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

// folderTagConsensus derives the one Artist+Album a folder's files agree
// they belong to. Deliberately strict, not a majority vote: every file
// with a non-empty Album tag must name the exact same album
// (case-insensitive), and every file with a non-empty AlbumArtist/Artist
// tag must name the exact same artist. A genuinely mixed folder (e.g.
// loose singles dropped directly in a root folder) fails this check
// outright rather than silently picking a plurality and mismatching
// whichever files disagree with it — see matchFolder's fallback.
func folderTagConsensus(entries []folderEntry) (artist, album string, ok bool) {
	for _, e := range entries {
		if a := strings.TrimSpace(e.tags.Album); a != "" {
			switch {
			case album == "":
				album = a
			case !strings.EqualFold(album, a):
				return "", "", false
			}
		}
		ar := strings.TrimSpace(e.tags.AlbumArtist)
		if ar == "" {
			ar = strings.TrimSpace(e.tags.Artist)
		}
		if ar != "" {
			switch {
			case artist == "":
				artist = ar
			case !strings.EqualFold(artist, ar):
				return "", "", false
			}
		}
	}
	if album == "" {
		return "", "", false // nothing to search a release by
	}
	return artist, album, true
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
func (s *Scanner) matchEntriesToRelease(ctx context.Context, entries []folderEntry, release *musicbrainz.ReleaseWithTracklist, confidence float64, result *ScanResult) {
	tracks := flattenTracks(release)
	used := make(map[int]bool, len(tracks)) // indices into tracks already claimed this pass, so two files can't both claim the same slot

	for _, e := range entries {
		idx, ft, ok := slotTrack(e.tags, tracks, used)
		if !ok {
			continue // left unmatched for manual review
		}
		used[idx] = true
		rec := recordingForReleaseTrack(ft, release)
		err := s.applyMatch(ctx, e.tf, rec, confidence, release.ID, ft.Position, ft.disc, database.StatusMatched)
		s.recordFileResult(ctx, result, e.tf, err == nil, err)
	}
}

// recordingForReleaseTrack shapes ft into a musicbrainz.Recording so it
// can be passed through applyMatch's existing, unmodified plumbing: since
// applyMatch resolves its target release via
// Recording.BestRelease(preferredReleaseMBID), it only needs
// Recording.Releases to contain release itself. ft.Recording already
// carries the real recording ID/Length from MusicBrainz; ArtistCredit is
// filled in from the release's own credit (inc=recordings does not
// re-embed a full recording body inside each track — only id/title/length).
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
