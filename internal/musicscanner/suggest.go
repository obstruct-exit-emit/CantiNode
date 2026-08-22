package musicscanner

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// TrackSuggestion is one proposed file→track slot from SuggestMatches, for
// a human to review and approve (via ManualMatch) before anything commits.
type TrackSuggestion struct {
	TrackFileID   int64  `json:"trackFileId"`
	RecordingMBID string `json:"recordingMbid"`
	ReleaseMBID   string `json:"releaseMbid"`
	TrackTitle    string `json:"trackTitle"`
	TrackNumber   int    `json:"trackNumber"`
	DiscNumber    int    `json:"discNumber"`
}

// SuggestMatches proposes, for each of fileIDs, which track within release
// its own already-read tags best correspond to — the exact same
// track-number-then-title-similarity algorithm (slotTrack) the automatic
// whole-folder scan uses (see matchFolder/matchEntriesToRelease), just
// returned as a proposal instead of applied: here a human picked the
// release by hand (the unmatched-files review page's own auto-match flow,
// scoped to one of their own artist's wanted/missing albums rather than a
// fresh MusicBrainz search), so nothing commits until they approve a
// suggestion and it's sent through ManualMatch like any other match. A
// file that can't be confidently slotted, or that's vanished since being
// listed, is simply omitted from the result rather than erroring the
// whole batch — the same non-aborting shape recordFileResult's callers
// already use for a scan.
func (s *Scanner) SuggestMatches(fileIDs []int64, release *musicbrainz.ReleaseWithTracklist) []TrackSuggestion {
	tracks := flattenTracks(release)
	used := make(map[int]bool, len(tracks))

	byID, err := s.db.ListTrackFilesByIDs(fileIDs)
	if err != nil {
		return []TrackSuggestion{}
	}

	out := []TrackSuggestion{}
	for _, id := range fileIDs {
		tf, ok := byID[id]
		if !ok {
			continue
		}
		var tags tagreader.Tags
		if tf.TagsJSON != "" {
			_ = json.Unmarshal([]byte(tf.TagsJSON), &tags)
		}
		idx, ft, ok := slotTrack(&tags, tracks, used)
		if !ok {
			continue
		}
		used[idx] = true
		rec := recordingForReleaseTrack(ft, release)
		out = append(out, TrackSuggestion{
			TrackFileID:   id,
			RecordingMBID: rec.ID,
			ReleaseMBID:   release.ID,
			TrackTitle:    rec.Title,
			TrackNumber:   ft.Position,
			DiscNumber:    ft.disc,
		})
	}
	return out
}

// UnmatchedFileGroup pairs an unmatched track_file with the folder "group
// key" auto-match and the automatic scanner both treat it as part of.
type UnmatchedFileGroup struct {
	musiclibrary.TrackFile
	// GroupKey is normally the file's own containing directory, but — for
	// files under sibling CD1/CD2/Disc-N subfolders of the same multi-disc
	// album — their shared parent directory instead, so the unmatched-files
	// review page groups exactly the same way ScanRootFolder's own
	// automatic matching does (see groupMultiDiscFolders). Absolute, kept
	// this way deliberately: two different root folders could otherwise
	// share a same-named subfolder and wrongly merge into one group if
	// this were root-relative instead.
	GroupKey string `json:"groupKey"`
	// GroupPath is GroupKey with its own root folder's path stripped off
	// the front — display-only, so the review page can show where a file
	// actually lives without the noise (or the accidental disk-layout
	// disclosure) of its full on-disk path. Empty when the file sits
	// directly in the root folder itself, or when its root folder
	// couldn't be resolved for some reason.
	GroupPath string `json:"groupPath"`
}

// ListUnmatchedWithGroups returns every currently unmatched track file,
// each tagged with its computed GroupKey — the unmatched-files review
// page's own folder grouping, kept in one place (here) rather than
// duplicating groupMultiDiscFolders' heuristic in the frontend.
func (s *Scanner) ListUnmatchedWithGroups() ([]UnmatchedFileGroup, error) {
	files, err := s.db.ListTrackFilesByStatus(musiclibrary.StatusUnmatched)
	if err != nil {
		return nil, fmt.Errorf("list unmatched track files: %w", err)
	}

	raw := map[string][]folderEntry{}
	for i := range files {
		tf := &files[i]
		var tags tagreader.Tags
		if tf.TagsJSON != "" {
			_ = json.Unmarshal([]byte(tf.TagsJSON), &tags)
		}
		dir := filepath.Dir(tf.Path)
		raw[dir] = append(raw[dir], folderEntry{tf: tf, tags: &tags})
	}
	grouped := groupMultiDiscFolders(raw, s.resolveArtistAlbumFallback)

	keyByID := make(map[int64]string, len(files))
	for key, entries := range grouped {
		for _, e := range entries {
			keyByID[e.tf.ID] = key
		}
	}

	rootPaths := make(map[int64]string) // cached per root folder, not re-queried per file
	out := make([]UnmatchedFileGroup, len(files))
	for i, f := range files {
		root, ok := rootPaths[f.RootFolderID]
		if !ok {
			if rf, err := s.db.GetRootFolder(f.RootFolderID); err == nil {
				root = filepath.Clean(rf.Path)
			}
			rootPaths[f.RootFolderID] = root
		}
		key := keyByID[f.ID]
		out[i] = UnmatchedFileGroup{TrackFile: f, GroupKey: key, GroupPath: rootRelativeDir(key, root)}
	}
	return out, nil
}

// rootRelativeDir strips root as a prefix from dir for display — dir
// itself (GroupKey) needs to stay the full absolute path for correctness
// (see its own doc comment), but showing that whole on-disk path in the
// UI is noisy and tells the user nothing about their actual library
// structure. "" both when the file sits directly in the root folder
// (nothing to show) and when root couldn't be resolved at all (falls
// back to the unmodified dir instead, never worse than before this
// existed).
func rootRelativeDir(dir, root string) string {
	if root == "" {
		return dir
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}
