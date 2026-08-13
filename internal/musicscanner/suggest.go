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
	// automatic matching does (see groupMultiDiscFolders).
	GroupKey string `json:"groupKey"`
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
	grouped := groupMultiDiscFolders(raw)

	keyByID := make(map[int64]string, len(files))
	for key, entries := range grouped {
		for _, e := range entries {
			keyByID[e.tf.ID] = key
		}
	}

	out := make([]UnmatchedFileGroup, len(files))
	for i, f := range files {
		out[i] = UnmatchedFileGroup{TrackFile: f, GroupKey: keyByID[f.ID]}
	}
	return out, nil
}
