package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
	"github.com/librinode/librinode/internal/scanner"
)

func seriesMediaType(v string) (string, bool) {
	return v, v == "manga" || v == "comic" || v == "magazine"
}

// handleSearchSeries proxies series search to the media type's provider
// (reached through GET /api/v1/search?type=manga|comic).
func (s *server) handleSearchSeries(w http.ResponseWriter, r *http.Request, mediaType, term string) {
	if mediaType == "magazine" {
		writeError(w, http.StatusBadRequest,
			"magazines have no metadata provider — add them by name under Series")
		return
	}
	p := s.metadata.SeriesFor(mediaType)
	if p == nil {
		writeError(w, http.StatusServiceUnavailable,
			"no "+mediaType+" metadata provider — pick one (and add its token/key) under Settings → Metadata")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	results, err := p.SearchSeries(ctx, term)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	mediaType := r.URL.Query().Get("mediaType")
	if mediaType != "" {
		if _, ok := seriesMediaType(mediaType); !ok {
			writeError(w, http.StatusBadRequest, "mediaType must be manga, comic, or magazine")
			return
		}
	}
	out := []library.Series{}
	for _, mt := range []string{"manga", "comic", "magazine"} {
		if mediaType != "" && mt != mediaType {
			continue
		}
		list, err := s.store.ListSeries(mt)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		out = append(out, list...)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddSeries syncs a manga/comic series (with all volumes) from its
// provider — or creates a magazine by name (magazines have no provider;
// issues materialize from grabs and scans). Like adding an author, this
// pulls metadata only: volumes start unmonitored (in the series' Missing
// section) and magazines don't auto-grab, until the user monitors volumes
// selectively or flips the series' monitor toggle. Explicit monitored/
// monitorNew in the request override that for API callers.
func (s *server) handleAddSeries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType       string `json:"mediaType"`
		ForeignSeriesID string `json:"foreignSeriesId"`
		Title           string `json:"title"`
		Monitored       *bool  `json:"monitored"`
		MonitorNew      *bool  `json:"monitorNew"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mediaType, ok := seriesMediaType(req.MediaType)
	if !ok {
		writeError(w, http.StatusBadRequest, "mediaType must be manga, comic, or magazine")
		return
	}
	monitored := false
	if req.Monitored != nil {
		monitored = *req.Monitored
	}
	monitorNew := monitored
	if req.MonitorNew != nil {
		monitorNew = *req.MonitorNew
	}

	if mediaType == "magazine" {
		title := strings.TrimSpace(req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required for magazines")
			return
		}
		series := &library.Series{
			Source:     "manual",
			ForeignID:  "magazine:" + scanner.Normalize(title),
			Title:      title,
			MediaType:  "magazine",
			Monitored:  monitored,
			MonitorNew: monitorNew,
		}
		if err := s.store.UpsertSeries(series); err != nil {
			writeStoreError(w, err)
			return
		}
		s.rematchFiles()
		s.writeSeriesDetail(w, http.StatusCreated, series.ID)
		return
	}

	if req.ForeignSeriesID == "" {
		writeError(w, http.StatusBadRequest, "foreignSeriesId is required")
		return
	}

	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	series, err := s.refresh.SyncSeries(ctx, mediaType, req.ForeignSeriesID, monitored, monitorNew, monitored)
	if err != nil {
		writeSeriesSyncError(w, err)
		return
	}
	s.rematchFiles()
	s.prefetchSeriesImages(series.ID)
	s.writeSeriesDetail(w, http.StatusCreated, series.ID)
}

// handleSeriesProvider sets (or with "" clears) the series' per-record
// provider override — it beats the global Settings → Metadata selection on
// the next refresh, including a global "none".
func (s *server) handleSeriesProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	series, err := s.store.GetSeries(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if req.Provider != "" && !slices.Contains(metadata.AvailableSeriesProviders(series.MediaType), req.Provider) {
		writeError(w, http.StatusBadRequest, "unknown "+series.MediaType+" provider: "+req.Provider)
		return
	}
	if err := s.store.SetSeriesProviderOverride(id, req.Provider); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeSeriesDetail(w, http.StatusOK, id)
}

func writeSeriesSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "no metadata provider configured for this media type")
	case errors.Is(err, metadata.ErrNotFound):
		writeError(w, http.StatusNotFound, "series not found at metadata provider")
	case errors.Is(err, library.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func (s *server) writeSeriesDetail(w http.ResponseWriter, status int, id int64) {
	series, err := s.store.GetSeries(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if series.Volumes, err = s.store.ListVolumes(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, status, series)
}

func (s *server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeSeriesDetail(w, http.StatusOK, id)
}

func (s *server) handleMonitorSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Monitored  bool `json:"monitored"`
		MonitorNew bool `json:"monitorNew"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetSeriesMonitored(id, req.Monitored, req.MonitorNew); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeSeriesDetail(w, http.StatusOK, id)
}

func (s *server) handleRefreshSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := s.metadataCtx(r)
	defer cancel()

	if err := s.refresh.RefreshSeries(ctx, id); err != nil {
		writeSeriesSyncError(w, err)
		return
	}
	s.rematchFiles()
	s.prefetchSeriesImages(id)
	s.writeSeriesDetail(w, http.StatusOK, id)
}

// handleSeriesSearch sweeps ONE series' wanted volumes/issues (monitored,
// missing their file) — the series page's Search wanted button.
func (s *server) handleSeriesSearch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	series, err := s.store.GetSeries(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Magazines are organize-only for now — no searching, no grabbing. Issues
	// still materialize from scans; acquisition returns in a later phase.
	if series.MediaType == "magazine" {
		writeJSON(w, http.StatusOK, map[string]any{
			"searched": 0, "grabbed": 0, "outcomes": []any{},
			"message": "magazine acquisition is disabled — the magazine library is organize-only for now",
		})
		return
	}
	volumes, err := s.store.ListVolumes(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	outcomes := []any{}
	searched, grabbed := 0, 0
	for i := range volumes {
		v := &volumes[i]
		if !v.Monitored || v.HasFile {
			continue
		}
		searched++
		o, err := s.search.SearchBook(r.Context(), v.ID, series.MediaType)
		if err != nil {
			outcomes = append(outcomes, map[string]any{
				"bookId": v.ID, "bookTitle": v.Title, "grabbed": false, "message": err.Error(),
			})
			continue
		}
		if o.Grabbed {
			grabbed++
		}
		outcomes = append(outcomes, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"searched": searched, "grabbed": grabbed, "outcomes": outcomes,
	})
}

func (s *server) handleDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	deleteFiles := wantsFileDeletion(r)
	var paths []string
	if deleteFiles {
		var err error
		if paths, err = s.store.FilePathsForSeries(id); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if err := s.store.DeleteSeries(id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.finishDelete(w, deleteFiles, paths)
}
