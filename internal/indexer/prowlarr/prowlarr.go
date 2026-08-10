// Package prowlarr is a native indexer source that searches a self-hosted
// Prowlarr instance (https://prowlarr.com) directly through its own
// GET /api/v1/search endpoint — the same call Prowlarr's own search page
// makes — instead of CantiNode pretending to be a Readarr application that
// Prowlarr pushes indexers into. One Prowlarr connection here fans out to
// every indexer configured *inside* Prowlarr; there's no per-indexer
// duplication in CantiNode's own settings, and no Readarr-shaped API
// surface for CantiNode to fake.
//
// A search result already carries a Prowlarr-proxied download link (routed
// back through Prowlarr, not the indexer directly). CantiNode's existing
// qBittorrent/SABnzbd clients already know how to resolve a generic
// download URL into a magnet or the actual .torrent/.nzb bytes (see
// internal/download's own resolve step, used identically for every
// Newznab/Torznab indexer's results), so no separate content-fetching
// logic is needed here — a Prowlarr release rides the exact same grab path
// as any other indexer's, and gets scored against quality profiles the
// same way too (Prowlarr's own search has no such concept).
package prowlarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/redact"
)

// defaultCategories mirrors every other indexer's own default (3010 =
// Audio/MP3, 3040 = Audio/Lossless) — the Settings UI has no per-category
// field for native sources today, so this is the only value in play.
const defaultCategories = "3010,3040"

// Def is the native-indexer definition; register it with
// indexer.RegisterNative at startup.
func Def() indexer.NativeDef {
	return indexer.NativeDef{
		Name:         "prowlarr",
		DisplayName:  "Prowlarr",
		Protocol:     indexer.ProtocolMixed, // a result's own Protocol carries the real value
		MediaTypes:   []string{"music"},
		NeedsBaseURL: true,
		NeedsAPIKey:  true,
		New: func(ind *indexer.Indexer, httpc *http.Client) indexer.Searcher {
			return &searcher{ind: ind, httpc: httpc}
		},
	}
}

type searcher struct {
	ind   *indexer.Indexer
	httpc *http.Client
}

func (s *searcher) baseURL() string {
	return strings.TrimRight(strings.TrimSpace(s.ind.BaseURL), "/")
}

func (s *searcher) categories() string {
	if cats := strings.TrimSpace(s.ind.AudioCategories); cats != "" {
		return cats
	}
	return defaultCategories
}

// Search queries Prowlarr's own /api/v1/search — Prowlarr fans out to every
// indexer it has configured and merges the results itself; CantiNode just
// asks once. A media type Prowlarr's music category can't serve yields
// nothing, not an error.
func (s *searcher) Search(ctx context.Context, query, mediaType string) ([]indexer.Release, error) {
	if mediaType != "music" {
		return nil, nil
	}
	if s.baseURL() == "" {
		return nil, fmt.Errorf("prowlarr: a base URL (your Prowlarr instance's address) is required")
	}

	q := url.Values{"query": {query}, "type": {"search"}}
	for _, cat := range strings.Split(s.categories(), ",") {
		if cat = strings.TrimSpace(cat); cat != "" {
			q.Add("categories", cat)
		}
	}

	body, err := s.get(ctx, "/api/v1/search?"+q.Encode())
	if err != nil {
		return nil, err
	}

	var results []release
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("prowlarr: parsing search response: %w", err)
	}

	releases := make([]indexer.Release, 0, len(results))
	for _, r := range results {
		releases = append(releases, r.toRelease(s.ind))
	}
	return releases, nil
}

// Test verifies the connection against Prowlarr's own system-status
// endpoint — cheap, authenticated, and doesn't touch any indexer Prowlarr
// itself has configured (a search does, and could trip a real indexer's
// own rate limit just to prove connectivity).
func (s *searcher) Test(ctx context.Context) error {
	if s.baseURL() == "" {
		return fmt.Errorf("prowlarr: a base URL (your Prowlarr instance's address) is required")
	}
	_, err := s.get(ctx, "/api/v1/system/status")
	return err
}

func (s *searcher) get(ctx context.Context, path string) ([]byte, error) {
	rawURL := s.baseURL() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", s.ind.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CantiNode")

	resp, err := s.httpc.Do(req)
	if err != nil {
		return nil, redact.URLError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("prowlarr: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr: HTTP %d: %.150s", resp.StatusCode, string(body))
	}
	return body, nil
}

// release is the subset of Prowlarr's own (much larger) ReleaseResource
// that CantiNode actually uses.
type release struct {
	GUID        string     `json:"guid"`
	Title       string     `json:"title"`
	Size        int64      `json:"size"`
	Indexer     string     `json:"indexer"`
	PublishDate time.Time  `json:"publishDate"`
	DownloadURL string     `json:"downloadUrl"`
	MagnetURL   string     `json:"magnetUrl"`
	InfoURL     string     `json:"infoUrl"`
	Protocol    protocol   `json:"protocol"`
	Seeders     *int       `json:"seeders"`
	Leechers    *int       `json:"leechers"`
	Categories  []category `json:"categories"`
}

type category struct {
	ID int `json:"id"`
}

// protocol tolerates Prowlarr's DownloadProtocol enum coming back as either
// a bare integer (the *arr family's C# enum: Usenet=1, Torrent=2) or its
// string name — observed to vary across Prowlarr versions/configurations.
// Anything else (including absent) decodes to "", handled the same as
// usenet by internal/release's scoring (a flat availability bonus, no
// seeder count expected).
type protocol string

func (p *protocol) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		switch strings.ToLower(s) {
		case "torrent":
			*p = protocol(indexer.ProtocolTorrent)
		case "usenet":
			*p = protocol(indexer.ProtocolUsenet)
		}
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		switch n {
		case 2:
			*p = protocol(indexer.ProtocolTorrent)
		case 1:
			*p = protocol(indexer.ProtocolUsenet)
		}
		return nil
	}
	return nil
}

// toRelease maps a Prowlarr result onto CantiNode's own Release shape,
// which the rest of the pipeline (scoring, grab, download-client routing)
// already treats every indexer's results the same way.
func (r release) toRelease(ind *indexer.Indexer) indexer.Release {
	// MagnetURL, when Prowlarr includes it, is directly usable with no HTTP
	// fetch at all; DownloadURL still needs resolving (redirect-to-magnet,
	// or the raw .torrent/.nzb bytes) — internal/download's own qBittorrent/
	// SABnzbd clients already do exactly that for every other indexer.
	downloadURL := r.MagnetURL
	if downloadURL == "" {
		downloadURL = r.DownloadURL
	}

	seeders, peers := -1, -1
	if r.Protocol == protocol(indexer.ProtocolTorrent) {
		if r.Seeders != nil {
			seeders = *r.Seeders
		}
		if r.Leechers != nil {
			peers = *r.Leechers
		}
	}

	cats := make([]int, 0, len(r.Categories))
	for _, c := range r.Categories {
		cats = append(cats, c.ID)
	}

	rel := indexer.Release{
		IndexerID: ind.ID,
		// Names which indexer inside Prowlarr actually answered — otherwise
		// every result would just say "Prowlarr" and be indistinguishable.
		Indexer:     ind.Name + " (" + r.Indexer + ")",
		Protocol:    string(r.Protocol),
		Title:       r.Title,
		GUID:        r.GUID,
		InfoURL:     r.InfoURL,
		DownloadURL: downloadURL,
		Size:        r.Size,
		Categories:  cats,
		Seeders:     seeders,
		Peers:       peers,
	}
	if !r.PublishDate.IsZero() {
		rel.PublishDate = r.PublishDate.UTC().Format(time.RFC3339)
	}
	return rel
}
