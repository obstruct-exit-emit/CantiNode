package refresh

import (
	"context"
	"fmt"
	"strings"

	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
	"github.com/librinode/librinode/internal/scanner"
)

// SyncSeries fetches a manga/comic series from its media type's provider and
// persists it with volume/issue books. Existing volumes keep their monitored
// flags (upsert semantics); newly discovered ones get newVolumesMonitored —
// the caller passes the add-time choice or, on refresh, the series'
// monitor_new flag ("monitor future volumes").
func (s *Service) SyncSeries(ctx context.Context, mediaType, foreignID string, monitored, monitorNew, newVolumesMonitored bool) (*library.Series, error) {
	p := s.providers.SeriesFor(mediaType)
	if p == nil {
		return nil, metadata.ErrNotConfigured
	}
	return s.syncSeriesWith(ctx, p, mediaType, foreignID, monitored, monitorNew, newVolumesMonitored)
}

// syncSeriesWith persists a series using the given provider — the caller picks
// it (the media type's active provider when adding, or the series' own source
// provider when refreshing, so a manga added from AniList keeps refreshing
// from AniList even after the manga provider is switched to Hardcover).
func (s *Service) syncSeriesWith(ctx context.Context, p metadata.SeriesProvider, mediaType, foreignID string, monitored, monitorNew, newVolumesMonitored bool) (*library.Series, error) {
	remote, err := p.GetSeries(ctx, foreignID)
	if err != nil {
		return nil, err
	}
	source := p.Name()

	// The creator gets an author stub (volumes need an author row); keyed by
	// name so one mangaka's series share it.
	authorName := remote.AuthorName
	if authorName == "" {
		authorName = "Unknown Creator"
	}
	author, err := s.store.GetAuthorByForeignID(source, "creator:"+scanner.Normalize(authorName))
	if err != nil {
		author = &library.Author{
			Source:    source,
			ForeignID: "creator:" + scanner.Normalize(authorName),
			Name:      authorName,
			Monitored: false,
		}
		if err := s.store.UpsertAuthor(author); err != nil {
			return nil, err
		}
	}

	series := &library.Series{
		Source:      source,
		ForeignID:   remote.ForeignID,
		Title:       remote.Title,
		Description: remote.Description,
		MediaType:   mediaType,
		Monitored:   monitored,
		MonitorNew:  monitorNew,
		CoverURL:    remote.CoverURL,
	}
	if err := s.store.UpsertSeries(series); err != nil {
		return nil, err
	}

	// Snapshot the volumes linked before this sync: any that the provider no
	// longer returns (a provider switch, or the preferred edition changing
	// with the metadata preferences) are retired afterwards.
	oldVolumes, err := s.store.ListVolumes(series.ID)
	if err != nil {
		return nil, err
	}
	oldPositions, err := s.store.SeriesBookPositions(series.ID)
	if err != nil {
		return nil, err
	}

	numberPrefix := "Vol. "
	if mediaType == "comic" {
		numberPrefix = "#"
	}
	for _, issue := range remote.Issues {
		volMonitored := newVolumesMonitored
		if existing, err := s.store.GetBookByForeignID(source, issue.ForeignID); err == nil {
			volMonitored = existing.Monitored // preserved by upsert anyway
		}
		// Use the volume's own description. Comics rarely carry a per-issue
		// synopsis, so the issue name stands in there; manga volumes are left
		// blank when the provider has none (AniList synthesizes volumes with
		// no descriptions) rather than repeating the series blurb on every one.
		description := issue.Description
		if description == "" && mediaType == "comic" {
			description = issue.Title
		}
		coverURL := issue.CoverURL
		if coverURL == "" {
			coverURL = remote.CoverURL
		}
		book := &library.Book{
			AuthorID:    author.ID,
			Source:      source,
			MediaType:   mediaType,
			ForeignID:   issue.ForeignID,
			Title:       fmt.Sprintf("%s %s%v", remote.Title, numberPrefix, trimFloat(issue.Number)),
			Description: description,
			ReleaseDate: issue.ReleaseDate,
			CoverURL:    coverURL,
			Monitored:   volMonitored,
		}
		if err := s.store.UpsertBook(book); err != nil {
			return nil, err
		}
		if err := s.store.LinkBookSeries(book.ID, series.ID, issue.Number); err != nil {
			return nil, err
		}
	}
	if err := s.retireStaleVolumes(source, remote, oldVolumes, oldPositions); err != nil {
		return nil, err
	}
	return series, nil
}

// retireStaleVolumes removes a series' volume books that the provider no
// longer returns — a provider switch retires the old provider's volumes, and
// a same-provider refresh retires editions the metadata preferences no
// longer pick. Each stale volume hands its files and monitored flag to the
// same-numbered replacement when one exists; an owned volume with no
// replacement is kept so its files aren't lost. Prose books linked to the
// series are left alone.
func (s *Service) retireStaleVolumes(source string, remote *metadata.SeriesResult, oldVolumes []library.Book, oldPositions map[int64]float64) error {
	keep := map[string]bool{}
	newByNumber := map[float64]int64{}
	for _, iss := range remote.Issues {
		keep[iss.ForeignID] = true
		if b, err := s.store.GetBookByForeignID(source, iss.ForeignID); err == nil {
			newByNumber[iss.Number] = b.ID
		}
	}
	for _, v := range oldVolumes {
		if v.MediaType == "book" {
			continue
		}
		if v.Source == source && keep[v.ForeignID] {
			continue // still part of the series
		}
		newID, hasNew := newByNumber[oldPositions[v.ID]]
		if hasNew && newID == v.ID {
			continue // it IS the replacement (paranoia guard)
		}
		hasFiles, err := s.store.BookHasFiles(v.ID)
		if err != nil {
			return err
		}
		if hasFiles && !hasNew {
			continue // no same-numbered replacement — keep the owned volume
		}
		if hasFiles {
			if err := s.store.MoveBookFiles(v.ID, newID); err != nil {
				return err
			}
		}
		if hasNew {
			// A replacement edition is not a "future volume": it inherits the
			// old edition's monitored state instead of the monitor-new default
			// the sync just gave it.
			if err := s.store.SetBookMonitored(newID, v.Monitored); err != nil {
				return err
			}
		}
		if err := s.store.DeleteBook(v.ID); err != nil {
			return err
		}
	}
	return nil
}

// trimFloat renders 5 as "5" and 5.5 as "5.5".
func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// RefreshSeries re-syncs an existing series; new volumes follow monitor_new.
// Manual series (magazines) have no provider to refresh — their issues come
// from grabs and scans — so they're a quiet no-op.
//
// Provider resolution: the series' own provider override wins when set;
// otherwise the global Settings → Metadata selection applies — INCLUDING
// "none", which means no metadata is fetched at all (libraries always honor
// the settings; the override is the explicit per-series escape hatch).
// Refreshing through a provider that differs from the series' source
// re-matches it by title and re-binds in place.
func (s *Service) RefreshSeries(ctx context.Context, id int64) error {
	series, err := s.store.GetSeries(id)
	if err != nil {
		return err
	}
	if series.Source == "manual" {
		return nil
	}
	p := s.providers.SeriesFor(series.MediaType)
	if series.ProviderOverride != "" {
		p = s.providers.SeriesProviderByName(series.ProviderOverride)
	}
	if p == nil {
		return nil // provider "none", unconfigured, or unknown override — leave the series as-is
	}
	if p.Name() == series.Source {
		_, err = s.syncSeriesWith(ctx, p, series.MediaType, series.ForeignID,
			series.Monitored, series.MonitorNew, series.MonitorNew)
		return err
	}
	// The resolved provider differs from the one this series was added with;
	// re-match it on the new provider and re-bind in place.
	return s.rematchSeries(ctx, series, p)
}

// rematchSeries moves a series to a different provider: it searches the active
// provider for the series title, re-binds the row to the best match (keeping
// the local id and monitoring), re-syncs volumes from the new provider, and
// drops the previous provider's volume books (owned volumes are kept so their
// files survive). If nothing matches, the series is left untouched rather than
// emptied.
func (s *Service) rematchSeries(ctx context.Context, series *library.Series, p metadata.SeriesProvider) error {
	results, err := p.SearchSeries(ctx, series.Title)
	if err != nil {
		return err
	}
	match := pickSeriesMatch(results, series.Title)
	if match == nil {
		return nil // no counterpart on the new provider — keep the old data
	}
	remote, err := p.GetSeries(ctx, match.ForeignID)
	if err != nil {
		return err
	}

	if err := s.store.RebindSeries(series.ID, p.Name(), remote.ForeignID,
		remote.Title, remote.Description, remote.CoverURL); err != nil {
		return err
	}
	series.Source = p.Name()
	series.ForeignID = remote.ForeignID

	// syncSeriesWith retires the old provider's volumes (files and monitored
	// flags migrate to the same-numbered new volumes) via retireStaleVolumes.
	_, err = s.syncSeriesWith(ctx, p, series.MediaType, remote.ForeignID,
		series.Monitored, series.MonitorNew, series.MonitorNew)
	return err
}

// pickSeriesMatch chooses the provider result that best corresponds to title:
// an exact case-insensitive title match wins; otherwise the first result, since
// providers return search hits by relevance.
func pickSeriesMatch(results []metadata.SeriesResult, title string) *metadata.SeriesResult {
	if len(results) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(title))
	for i := range results {
		if strings.ToLower(strings.TrimSpace(results[i].Title)) == want {
			return &results[i]
		}
	}
	return &results[0]
}

// refreshAllSeries re-syncs every manga/comic series (called from RefreshAll).
func (s *Service) refreshAllSeries(ctx context.Context) {
	for _, mediaType := range []string{"manga", "comic"} {
		seriesList, err := s.store.ListSeries(mediaType)
		if err != nil {
			continue
		}
		var unreachable unreachableStreak
		for _, sr := range seriesList {
			if ctx.Err() != nil {
				return
			}
			err := s.RefreshSeries(ctx, sr.ID)
			if unreachable.hit(err) {
				// The manga/comic provider stopped responding partway
				// through — move to the next media type rather than timing
				// out on every remaining series.
				break
			}
			// one dead record (err != nil, not a streak) can't stall the rest
		}
	}
}
