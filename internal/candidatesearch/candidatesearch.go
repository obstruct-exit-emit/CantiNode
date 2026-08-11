// Package candidatesearch is the one "search indexers, drop blocklisted
// results, score, rank" pipeline every release search in CantiNode goes
// through — a manual "Search releases"/"Search upgrade" click and the
// automatic wanted-list sweep (internal/autosearch) alike — so a future
// change to blocklist matching, scoring order, or partial-indexer-failure
// handling only has to be made once. Before this package existed the same
// five steps were copy-pasted three times across internal/api and
// internal/autosearch, and had already drifted once: the manual search
// handlers surfaced indexer errors to their caller, autosearch silently
// discarded them.
package candidatesearch

import (
	"context"

	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/release"
)

// Search runs indexers.SearchAll, then ScoreAndRank — the one-shot shape a
// manual search endpoint wants, where fetching the blocklist fresh on every
// call is irrelevant cost. errs reports which indexers failed to answer;
// non-nil (though possibly empty) whenever err is nil.
func Search(
	ctx context.Context,
	indexers *indexer.Service,
	downloads *download.Service,
	query, nativeQuery, mediaType string,
	prefs release.Preferences,
) (candidates []release.Candidate, errs []string, err error) {
	found, errs, err := indexers.SearchAll(ctx, query, nativeQuery, mediaType)
	if err != nil {
		return nil, errs, err
	}
	blocked, err := downloads.Store().BlockedKeys()
	if err != nil {
		return nil, errs, err
	}
	return ScoreAndRank(found, blocked, prefs), errs, nil
}

// ScoreAndRank filters blocklisted releases out of found, scores the rest
// against prefs, and ranks them (approved first, then by score). Exported
// separately from Search so a caller sweeping many searches in one pass
// (internal/autosearch) can fetch the blocklist and preferences once for
// the whole sweep instead of paying for both fresh on every single search.
func ScoreAndRank(found []indexer.Release, blocked map[string]bool, prefs release.Preferences) []release.Candidate {
	candidates := make([]release.Candidate, 0, len(found))
	for _, rel := range found {
		if download.IsBlocked(blocked, rel.GUID, rel.Title) {
			continue
		}
		candidates = append(candidates, release.Score(rel, prefs))
	}
	release.Rank(candidates)
	return candidates
}
