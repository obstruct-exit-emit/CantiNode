// Package prowlarr is a client for a self-hosted Prowlarr instance
// (https://prowlarr.com) — CantiNode's indexer search layer. Search
// results are fetched via GET /api/v1/search; CantiNode then resolves a
// chosen release's own content (a magnet URI, or the actual .torrent/
// .nzb bytes) itself via FetchContent and hands it to internal/qbittorrent
// or internal/sabnzbd — deliberately not Prowlarr's own POST
// /api/v1/search "grab" endpoint, which pushes to whatever download
// client is configured inside Prowlarr's own settings. Going through
// Prowlarr's own download-client config would work too, but would make
// the download client's involvement implicit and dependent on how the
// user has Prowlarr itself configured; CantiNode owns the download-client
// relationship directly instead, the same way it owns MusicBrainz and
// Cover Art Archive.
package prowlarr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultCategoryMusic = 3000

// Client is a Prowlarr API client.
type Client struct {
	httpClient           *http.Client
	noRedirectHTTPClient *http.Client
	baseURL              string
	apiKey               string
	userAgent            string
}

// NewClient returns a Client against a self-hosted Prowlarr at baseURL
// (e.g. "http://localhost:9696"), authenticating with apiKey (Prowlarr's
// own X-Api-Key header convention, same as the rest of the *arr family).
func NewClient(baseURL, apiKey, userAgent string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		// Redirects followed manually by FetchContent — see its own doc
		// comment for why (distinguishing a magnet: redirect, which isn't
		// fetchable content, from an http(s) one that needs following).
		noRedirectHTTPClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL:   baseURL,
		apiKey:    apiKey,
		userAgent: userAgent,
	}
}

// Search queries every indexer Prowlarr has configured for query,
// scoped to the Music category (and its subcategories, per Newznab/
// Torznab convention) unless categories is non-empty.
func (c *Client) Search(ctx context.Context, query string, categories ...int) ([]Release, error) {
	if len(categories) == 0 {
		categories = []int{defaultCategoryMusic}
	}

	q := url.Values{"query": {query}, "type": {"search"}}
	for _, cat := range categories {
		q.Add("categories", strconv.Itoa(cat))
	}

	body, err := c.get(ctx, "/api/v1/search?"+q.Encode())
	if err != nil {
		return nil, err
	}

	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return releases, nil
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body for %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prowlarr %s: status %d: %s", path, resp.StatusCode, truncate(string(data), 300))
	}
	return data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
