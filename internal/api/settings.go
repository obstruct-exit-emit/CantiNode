package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/metadata"
	"github.com/librinode/librinode/internal/naming"
)

// metadataSettingsResponse is the settings UI's view of metadata config:
// which providers exist, which one is active, and their stored settings.
type metadataSettingsResponse struct {
	Active          string   `json:"active"`
	Available       []string `json:"available"`
	SeriesAvailable []string `json:"seriesAvailable"`
	// Fallbacks is the ordered list of book providers consulted when Active
	// finds nothing; a subset of Available, never including Active itself.
	Fallbacks []string                     `json:"fallbacks"`
	Providers map[string]metadata.Settings `json:"providers"`
	// MangaProviders / ComicProviders list the providers that can serve each
	// series media type (for the selectors); the singular fields are the
	// chosen ones.
	MangaProviders []string `json:"mangaProviders"`
	MangaProvider  string   `json:"mangaProvider"`
	ComicProviders []string `json:"comicProviders"`
	ComicProvider  string   `json:"comicProvider"`
	// Per-library volume-cover source, "file" or "provider" (effective
	// values — defaults applied).
	MangaCoverSource string `json:"mangaCoverSource"`
	ComicCoverSource string `json:"comicCoverSource"`
	// Global, provider-agnostic metadata preferences (effective values).
	Language            string `json:"language"`
	Country             string `json:"country"`
	IncludeAdult        bool   `json:"includeAdult"`
	IncludeCompilations bool   `json:"includeCompilations"`
}

func (s *server) metadataSettingsResponse() metadataSettingsResponse {
	ms := s.cfg.MetadataSettings()
	resp := metadataSettingsResponse{
		Active:              ms.Active,
		Available:           metadata.Available(),
		SeriesAvailable:     metadata.SeriesAvailable(),
		Fallbacks:           ms.Fallbacks,
		Providers:           ms.Providers,
		MangaProviders:      metadata.AvailableSeriesProviders("manga"),
		MangaProvider:       s.cfg.MangaSeriesProvider(),
		ComicProviders:      metadata.AvailableSeriesProviders("comic"),
		ComicProvider:       s.cfg.ComicSeriesProvider(),
		MangaCoverSource:    s.cfg.CoverSourceFor("manga"),
		ComicCoverSource:    s.cfg.CoverSourceFor("comic"),
		Language:            s.cfg.MetadataLanguage(),
		Country:             s.cfg.MetadataCountry(),
		IncludeAdult:        s.cfg.IncludeAdult(),
		IncludeCompilations: s.cfg.IncludeCompilations(),
	}
	if resp.Fallbacks == nil {
		resp.Fallbacks = []string{}
	}
	// Every registered provider shows up in the form, configured or not.
	for _, name := range append(append([]string{}, resp.Available...), resp.SeriesAvailable...) {
		if _, ok := resp.Providers[name]; !ok {
			resp.Providers[name] = metadata.Settings{}
		}
	}
	return resp
}

func validCoverSource(v string) bool {
	return v == "" || v == "file" || v == "provider"
}

func (s *server) handleGetMetadataSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metadataSettingsResponse())
}

// handlePutMetadataSettings saves provider settings, persists them to
// config.yaml, and hot-swaps the active provider — no restart needed.
func (s *server) handlePutMetadataSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active              string                       `json:"active"`
		Fallbacks           []string                     `json:"fallbacks"`
		Providers           map[string]metadata.Settings `json:"providers"`
		MangaProvider       string                       `json:"mangaProvider"`
		ComicProvider       string                       `json:"comicProvider"`
		MangaCoverSource    string                       `json:"mangaCoverSource"`
		ComicCoverSource    string                       `json:"comicCoverSource"`
		Language            string                       `json:"language"`
		Country             string                       `json:"country"`
		IncludeAdult        bool                         `json:"includeAdult"`
		IncludeCompilations bool                         `json:"includeCompilations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Active != "" && !slices.Contains(metadata.Available(), req.Active) {
		writeError(w, http.StatusBadRequest, "unknown provider: "+req.Active)
		return
	}
	// Fallbacks must be known book providers, and never the active one (it is
	// always tried first — listing it as a fallback too is meaningless).
	fallbacks := make([]string, 0, len(req.Fallbacks))
	seenFallback := map[string]bool{}
	for _, fb := range req.Fallbacks {
		if fb == "" || fb == req.Active || seenFallback[fb] {
			continue
		}
		if !slices.Contains(metadata.Available(), fb) {
			writeError(w, http.StatusBadRequest, "unknown fallback provider: "+fb)
			return
		}
		seenFallback[fb] = true
		fallbacks = append(fallbacks, fb)
	}
	if req.MangaProvider != "" && req.MangaProvider != "none" &&
		!slices.Contains(metadata.AvailableSeriesProviders("manga"), req.MangaProvider) {
		writeError(w, http.StatusBadRequest, "unknown manga provider: "+req.MangaProvider)
		return
	}
	if req.ComicProvider != "" && req.ComicProvider != "none" &&
		!slices.Contains(metadata.AvailableSeriesProviders("comic"), req.ComicProvider) {
		writeError(w, http.StatusBadRequest, "unknown comic provider: "+req.ComicProvider)
		return
	}
	if !validCoverSource(req.MangaCoverSource) || !validCoverSource(req.ComicCoverSource) {
		writeError(w, http.StatusBadRequest, "cover source must be file or provider")
		return
	}
	if req.Providers == nil {
		req.Providers = map[string]metadata.Settings{}
	}

	ms := config.MetadataSettings{
		Active:              req.Active,
		Fallbacks:           fallbacks,
		Providers:           req.Providers,
		MangaProvider:       req.MangaProvider,
		ComicProvider:       req.ComicProvider,
		MangaCoverSource:    req.MangaCoverSource,
		ComicCoverSource:    req.ComicCoverSource,
		Language:            strings.ToLower(strings.TrimSpace(req.Language)),
		Country:             strings.ToLower(strings.TrimSpace(req.Country)),
		IncludeAdult:        req.IncludeAdult,
		IncludeCompilations: req.IncludeCompilations,
	}
	// Build with the global preferences injected (same shape ProviderSettings
	// produces once the config is saved); "none" means no preference.
	lang, country := ms.Language, ms.Country
	if lang == "none" {
		lang = ""
	}
	if country == "none" {
		country = ""
	}
	injected := make(map[string]metadata.Settings, len(ms.Providers))
	for name, ps := range ms.Providers {
		ps.Language, ps.Country, ps.IncludeAdult = lang, country, ms.IncludeAdult
		ps.IncludeCompilations = ms.IncludeCompilations
		injected[name] = ps
	}
	if err := s.metadata.ConfigureWithFallbacks(ms.Active, ms.Fallbacks, injected); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.SetMetadata(ms); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	s.metadata.ConfigureSeries(s.cfg.ProviderSettings(), s.cfg.SeriesSelection())
	s.refreshHealth()
	writeJSON(w, http.StatusOK, s.metadataSettingsResponse())
}

// --- Naming settings ---

// exampleTokenData renders template previews with a recognizable book.
var exampleTokenData = naming.TokenData{
	AuthorName:     "Terry Pratchett",
	AuthorSortName: "Pratchett, Terry",
	BookTitle:      "The Colour of Magic",
	SeriesTitle:    "Discworld",
	SeriesPosition: 1,
	ReleaseYear:    "1983",
}

type namingSettingsResponse struct {
	config.NamingSettings
	Tokens           []string `json:"tokens"`
	Example          string   `json:"example"`
	AudiobookExample string   `json:"audiobookExample"`
}

func namingResponse(ns config.NamingSettings) namingSettingsResponse {
	audiobookDir := naming.Format(ns.AudiobookFile, exampleTokenData)
	return namingSettingsResponse{
		NamingSettings: ns,
		Tokens:         naming.Tokens,
		Example: filepath.ToSlash(filepath.Join(
			naming.FormatPath(ns.EbookFolder, exampleTokenData),
			naming.Format(ns.EbookFile, exampleTokenData)+".epub",
		)),
		AudiobookExample: filepath.ToSlash(filepath.Join(
			naming.FormatPath(ns.AudiobookFolder, exampleTokenData),
			audiobookDir,
			audiobookDir+".m4b",
		)),
	}
}

func (s *server) handleGetNamingSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, namingResponse(s.cfg.NamingSettings()))
}

func (s *server) handlePutNamingSettings(w http.ResponseWriter, r *http.Request) {
	var req config.NamingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Empty fields fall back to defaults (SetNaming fills them), so partial
	// payloads can never wipe another media type's templates.
	if err := s.cfg.SetNaming(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, namingResponse(s.cfg.NamingSettings()))
}

// --- Import settings ---

func (s *server) handleGetImportSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.ImportSettings())
}

func (s *server) handlePutImportSettings(w http.ResponseWriter, r *http.Request) {
	var req config.ImportSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetImport(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.ImportSettings())
}

// --- Remote path mappings ---

func (s *server) handleGetPathMappings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.PathMappings())
}

// handlePutPathMappings replaces the whole mapping list (the UI edits it as
// one small table).
func (s *server) handlePutPathMappings(w http.ResponseWriter, r *http.Request) {
	var req []config.PathMapping
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetPathMappings(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.PathMappings())
}

// --- Background timing settings ---

func (s *server) handleGetTimingSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.TimingSettings())
}

// handlePutTimingSettings saves the background-loop cadences. Values are
// clamped by SetTimings; changes take effect on the next server start.
func (s *server) handlePutTimingSettings(w http.ResponseWriter, r *http.Request) {
	var req config.TimingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.SetTimings(req); err != nil {
		writeError(w, http.StatusInternalServerError, "saving config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.TimingSettings())
}

// handleTestMetadataProvider builds a provider from the submitted (unsaved)
// settings and checks it against the live API.
func (s *server) handleTestMetadataProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string            `json:"provider"`
		Settings metadata.Settings `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}

	p, err := metadata.Build(req.Provider, req.Settings)
	if errors.Is(err, metadata.ErrNotConfigured) {
		writeError(w, http.StatusBadRequest, "provider is not configured — enter a token first")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if v, ok := p.(metadata.Validator); ok {
		err = v.Validate(ctx)
	} else {
		// No cheap validation call — a tiny search exercises auth instead.
		_, err = p.SearchBooks(ctx, "test")
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": req.Provider})
}

// handleClearMetadataCache deletes the downloaded provider images (author
// portraits and cover art). They re-download from the provider as pages are
// viewed; this just reclaims disk (or forces fresh art).
func (s *server) handleClearMetadataCache(w http.ResponseWriter, r *http.Request) {
	removed, freed, err := s.images.Clear()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "freedBytes": freed})
}
