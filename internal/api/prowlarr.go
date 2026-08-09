package api

// Prowlarr application sync: Prowlarr only pushes indexers to *arr apps it
// recognizes, so LibriNode speaks Readarr's v1 API dialect on the indexer
// endpoints — add LibriNode to Prowlarr as a "Readarr" application and it
// manages indexers here automatically. The same endpoints keep accepting
// LibriNode's native JSON; the payloads are distinguished by the arr-style
// "implementation" marker.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
)

// arrField is the name/value pair *arr resources use for provider settings.
type arrField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// arrIndexerResource is the Readarr v1 indexer shape (the subset Prowlarr
// reads and writes).
type arrIndexerResource struct {
	ID                      int64      `json:"id,omitempty"`
	Name                    string     `json:"name"`
	Implementation          string     `json:"implementation"`
	ImplementationName      string     `json:"implementationName,omitempty"`
	ConfigContract          string     `json:"configContract"`
	Protocol                string     `json:"protocol,omitempty"`
	SupportsRss             bool       `json:"supportsRss"`
	SupportsSearch          bool       `json:"supportsSearch"`
	EnableRss               bool       `json:"enableRss"`
	EnableAutomaticSearch   bool       `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool       `json:"enableInteractiveSearch"`
	Priority                int        `json:"priority"`
	Tags                    []int      `json:"tags"`
	Fields                  []arrField `json:"fields"`
}

func (r *arrIndexerResource) field(name string) any {
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value
		}
	}
	return nil
}

func (r *arrIndexerResource) stringField(name string) string {
	if v, ok := r.field(name).(string); ok {
		return v
	}
	return ""
}

// toIndexer maps an arr resource onto LibriNode's indexer model.
func (r *arrIndexerResource) toIndexer() (*indexer.Indexer, error) {
	ind := &indexer.Indexer{
		ID:       r.ID,
		Name:     strings.TrimSpace(r.Name),
		Priority: r.Priority,
		Enabled:  r.EnableRss || r.EnableAutomaticSearch || r.EnableInteractiveSearch,
	}
	switch strings.ToLower(r.Implementation) {
	case indexer.TypeNewznab:
		ind.Type = indexer.TypeNewznab
	case indexer.TypeTorznab:
		ind.Type = indexer.TypeTorznab
	default:
		return nil, fmt.Errorf("unsupported implementation %q", r.Implementation)
	}

	base := strings.TrimRight(strings.TrimSpace(r.stringField("baseUrl")), "/")
	if base == "" {
		return nil, fmt.Errorf("baseUrl field is required")
	}
	if apiPath := strings.TrimSpace(r.stringField("apiPath")); apiPath != "" && apiPath != "/api" {
		base += apiPath
	}
	ind.BaseURL = base
	ind.APIKey = r.stringField("apiKey")

	if cats, ok := r.field("categories").([]any); ok {
		parts := make([]string, 0, len(cats))
		for _, c := range cats {
			if n, ok := c.(float64); ok {
				parts = append(parts, strconv.Itoa(int(n)))
			}
		}
		ind.Categories = strings.Join(parts, ",")
	}
	if ind.Categories == "" {
		ind.Categories = "7000,7020"
	}
	// Prowlarr's Readarr dialect has no audio/comic/magazine category
	// fields; defaults.
	ind.AudioCategories = "3030"
	ind.ComicCategories = "7030"
	ind.MagazineCategories = "7010"
	if ind.Priority <= 0 || ind.Priority > 50 {
		ind.Priority = 25
	}
	return ind, nil
}

// toArrResource maps a stored indexer into the Readarr v1 shape.
func toArrResource(ind *indexer.Indexer) arrIndexerResource {
	impl := "Newznab"
	if ind.Type == indexer.TypeTorznab {
		impl = "Torznab"
	}
	categories := []int{}
	for _, part := range strings.Split(ind.Categories, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			categories = append(categories, n)
		}
	}
	fields := []arrField{
		{Name: "baseUrl", Value: ind.BaseURL},
		{Name: "apiPath", Value: "/api"},
		{Name: "apiKey", Value: ind.APIKey},
		{Name: "categories", Value: categories},
		{Name: "additionalParameters", Value: ""},
	}
	if ind.Type == indexer.TypeTorznab {
		fields = append(fields, arrField{Name: "minimumSeeders", Value: 1})
	}
	return arrIndexerResource{
		ID:                      ind.ID,
		Name:                    ind.Name,
		Implementation:          impl,
		ImplementationName:      impl,
		ConfigContract:          impl + "Settings",
		Protocol:                ind.Protocol(),
		SupportsRss:             true,
		SupportsSearch:          true,
		EnableRss:               ind.Enabled,
		EnableAutomaticSearch:   ind.Enabled,
		EnableInteractiveSearch: ind.Enabled,
		Priority:                ind.Priority,
		Tags:                    []int{},
		Fields:                  fields,
	}
}

// mergedIndexerResource serves both consumers from one endpoint: LibriNode's
// UI reads the flat native keys, Prowlarr reads the arr keys.
func mergedIndexerResource(ind *indexer.Indexer) map[string]any {
	merged := map[string]any{}

	native, _ := json.Marshal(ind)
	_ = json.Unmarshal(native, &merged)

	arr, _ := json.Marshal(toArrResource(ind))
	arrMap := map[string]any{}
	_ = json.Unmarshal(arr, &arrMap)
	for k, v := range arrMap {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	return merged
}

// handleIndexerSchema lists the indexer implementations Prowlarr may push.
func (s *server) handleIndexerSchema(w http.ResponseWriter, r *http.Request) {
	schema := []arrIndexerResource{
		{
			Name: "", Implementation: "Newznab", ImplementationName: "Newznab",
			ConfigContract: "NewznabSettings", Protocol: indexer.ProtocolUsenet,
			SupportsRss: true, SupportsSearch: true, Tags: []int{},
			Fields: []arrField{
				{Name: "baseUrl"}, {Name: "apiPath", Value: "/api"}, {Name: "apiKey"},
				{Name: "categories", Value: []int{7000, 7020}}, {Name: "additionalParameters"},
			},
		},
		{
			Name: "", Implementation: "Torznab", ImplementationName: "Torznab",
			ConfigContract: "TorznabSettings", Protocol: indexer.ProtocolTorrent,
			SupportsRss: true, SupportsSearch: true, Tags: []int{},
			Fields: []arrField{
				{Name: "baseUrl"}, {Name: "apiPath", Value: "/api"}, {Name: "apiKey"},
				{Name: "categories", Value: []int{7000, 7020}}, {Name: "additionalParameters"},
				{Name: "minimumSeeders", Value: 1},
				// Torrent seed-management fields real Readarr's Torznab schema
				// carries; Prowlarr populates these when building a torrent
				// indexer, and a template missing them makes it dereference null.
				{Name: "seedCriteria.seedRatio"},
				{Name: "seedCriteria.seedTime"},
				{Name: "seedCriteria.discographySeedTime"},
				{Name: "rejectBlocklistedTorrentHashesWhileGrabbing", Value: false},
			},
		},
	}
	writeJSON(w, http.StatusOK, schema)
}

// handleListTags exists because *arr clients resolve tags during sync;
// LibriNode has no tag system yet.
func (s *server) handleListTags(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// handleListMetadataProfiles serves Readarr's metadata-profile list — a
// books-only concept Prowlarr's Readarr proxy reads during application sync.
// Without it Prowlarr got a 404 where it expected a profile array and threw
// a NullReferenceException (which is why Sonarr/Radarr synced but the Readarr
// app didn't). LibriNode has no metadata profiles, so one static default is
// enough for Prowlarr to reference.
func (s *server) handleListMetadataProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]any{{
		"id":                  1,
		"name":                "Standard",
		"minPopularity":       0,
		"skipMissingDate":     false,
		"skipMissingIsbn":     false,
		"skipPartsAndSets":    false,
		"skipSeriesSecondary": false,
		"allowedLanguages":    "",
		"minPages":            0,
		"ignored":             "",
	}})
}

// --- Readarr-compatible capability resources ---
//
// During an application sync Prowlarr reads the target's root folders,
// quality profiles, and download clients (its Readarr proxy deserializes
// them into Readarr resources and dereferences fields). LibriNode's native
// JSON lacks those fields, so Prowlarr threw a NullReferenceException and
// aborted before syncing any indexer. These endpoints merge the Readarr
// fields onto the native ones — LibriNode's own UI keeps reading its native
// keys, Prowlarr gets a shape it can parse. Crucially, download clients
// carry a `protocol`, so Prowlarr sees the torrent client and will sync
// torrent (Torznab) indexers, not just usenet.

// isProwlarr reports whether a request comes from Prowlarr (its HTTP client
// sends a "Prowlarr/…" User-Agent). LibriNode's own web UI is a browser, so
// this cleanly separates the two consumers — the capability endpoints serve
// Readarr-shaped resources to Prowlarr and native JSON to everything else,
// avoiding field-type clashes (e.g. quality-profile cutoff: string vs int).
func isProwlarr(r *http.Request) bool {
	return strings.Contains(r.Header.Get("User-Agent"), "Prowlarr")
}

// mergeArr overlays arr-style fields onto a native resource without clobbering
// keys the native shape already provides.
func mergeArr(native any, arr map[string]any) map[string]any {
	merged := map[string]any{}
	raw, _ := json.Marshal(native)
	_ = json.Unmarshal(raw, &merged)
	for k, v := range arr {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	return merged
}

func readarrRootFolder(f rootFolder) map[string]any {
	name := filepath.Base(f.Path)
	if name == "" || name == "." || name == "/" {
		name = f.MediaType
	}
	return mergeArr(f, map[string]any{
		"name":                        name,
		"accessible":                  f.Accessible,
		"freeSpace":                   0,
		"totalSpace":                  0,
		"defaultQualityProfileId":     1,
		"defaultMetadataProfileId":    1,
		"defaultMonitorOption":        "all",
		"defaultNewItemMonitorOption": "all",
		"defaultTags":                 []int{},
		"isCalibreLibrary":            false,
	})
}

func readarrQualityProfile(p library.QualityProfile) map[string]any {
	// Readarr resolves `cutoff` (a quality id) against `items`, so an empty
	// items list with a non-zero cutoff makes Prowlarr's proxy dereference a
	// null. Provide a single quality whose id matches the cutoff.
	items := []any{
		map[string]any{
			"quality": map[string]any{"id": 1, "name": "eBook"},
			"items":   []any{},
			"allowed": true,
		},
	}
	return mergeArr(p, map[string]any{
		"upgradeAllowed":    p.UpgradesAllowed,
		"cutoff":            1,
		"minFormatScore":    0,
		"cutoffFormatScore": 0,
		"items":             items,
		"formatItems":       []any{},
	})
}

func readarrDownloadClient(c download.ClientConfig) map[string]any {
	impl := "Sabnzbd"
	contract := "SabnzbdSettings"
	if c.Protocol() == download.ProtocolTorrent {
		impl = "QBittorrent"
		contract = "QBittorrentSettings"
	}
	return mergeArr(c, map[string]any{
		"protocol":                 c.Protocol(), // "usenet" | "torrent"
		"enable":                   c.Enabled,
		"priority":                 c.Priority,
		"implementation":           impl,
		"implementationName":       impl,
		"configContract":           contract,
		"fields":                   []any{},
		"tags":                     []int{},
		"categories":               []any{},
		"supportsCategories":       false,
		"removeCompletedDownloads": false,
		"removeFailedDownloads":    false,
	})
}
