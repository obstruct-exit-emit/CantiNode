package musicscanner

import (
	"encoding/json"

	"github.com/cantinode/cantinode/internal/musicbrainz"
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

	out := []TrackSuggestion{}
	for _, id := range fileIDs {
		tf, err := s.db.GetTrackFile(id)
		if err != nil {
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
