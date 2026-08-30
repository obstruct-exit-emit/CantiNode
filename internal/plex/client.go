// Package plex is a thin client for the Plex Media Server API
// (https://support.plex.tv/articles/201638786-plex-media-server-url-commands/).
// Two independent capabilities: pushing a "refresh this path" notification
// whenever CantiNode adds, moves, or removes files on disk (client.go,
// notify.go — the same pattern Sonarr/Radarr/Lidarr call a "Plex Media
// Server" connection), and the playlist CRUD/lookup primitives
// internal/plexplaylistsync's two-way sync builds on (playlist.go).
// Deliberately narrow either way — not a general Plex API client.
package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one Plex Media Server, authenticated with its own
// X-Plex-Token (Settings → Plex → Server URL/Token).
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// NewClient returns a Client for serverURL (e.g. "http://192.168.1.10:32400"),
// authenticated with token.
func NewClient(serverURL, token string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    strings.TrimRight(serverURL, "/"),
		token:      token,
	}
}

// Section is one of Plex's own library sections (GET /library/sections) —
// Type "artist" is Plex's own internal name for a music library, the only
// kind CantiNode's own Settings picker offers.
type Section struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
	// Paths are this section's own library folder(s), exactly as Plex
	// itself sees them (a section can span more than one folder) —
	// surfaced so Settings can show the operator what path Plex actually
	// expects, right next to the path mapping field that needs to match
	// it. Found live: a refresh call against a path Plex doesn't
	// recognize at all returns the exact same 200 OK a real, effective
	// one does, so there's no other way to catch a wrong or missing
	// mapping before it silently does nothing.
	Paths []string `json:"paths"`
}

type sectionsResponse struct {
	MediaContainer struct {
		Directory []struct {
			Key      string `json:"key"`
			Title    string `json:"title"`
			Type     string `json:"type"`
			Location []struct {
				Path string `json:"path"`
			} `json:"Location"`
		} `json:"Directory"`
	} `json:"MediaContainer"`
}

// MusicSections returns every music ("artist"-type) library section this
// server knows about — the Settings picker's own data source, and a
// side-effect-free way to verify the server URL and token both work
// (Settings' own Test button).
func (c *Client) MusicSections(ctx context.Context) ([]Section, error) {
	body, err := c.get(ctx, "/library/sections", nil)
	if err != nil {
		return nil, err
	}
	var resp sectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode sections: %w", err)
	}
	var out []Section
	for _, d := range resp.MediaContainer.Directory {
		if d.Type != "artist" {
			continue
		}
		paths := make([]string, 0, len(d.Location))
		for _, loc := range d.Location {
			paths = append(paths, loc.Path)
		}
		out = append(out, Section{Key: d.Key, Title: d.Title, Type: d.Type, Paths: paths})
	}
	return out, nil
}

// RefreshPath asks Plex to do a partial scan of path within sectionKey's
// own library — much cheaper than a full section scan (GET
// /library/sections/{key}/refresh with no path), and Plex reconciles
// removed files the same way a full scan would: a path that no longer
// exists on disk at all is scanned as empty and any Plex items it used to
// back are cleaned up. path is CantiNode's own idea of the path — callers
// translate it through the configured Plex path mappings first (see
// config.PlexSettings.PathMappings) when Plex sees this share mounted
// somewhere else.
func (c *Client) RefreshPath(ctx context.Context, sectionKey, path string) error {
	if sectionKey == "" {
		return fmt.Errorf("plex: section key is required")
	}
	_, err := c.get(ctx, "/library/sections/"+url.PathEscape(sectionKey)+"/refresh", url.Values{"path": {path}})
	return err
}

func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, query)
}

// post issues a POST — creating a playlist (POST /playlists) is the one
// call in this client that isn't safe as a GET: found live against a real
// server (Plex Media Server 1.43.3), which answers a GET to that endpoint
// with a bare 500 and no detail, while the identical query as a POST
// succeeds normally.
func (c *Client) post(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, query)
}

// put issues a PUT — Plex's own API uses it for both "add these items"
// (POST-like) and "rename this" (PATCH-like) actions, always via query
// parameters rather than a request body.
func (c *Client) put(ctx context.Context, path string, query url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodPut, path, query)
}

func (c *Client) delete(ctx context.Context, path string) error {
	_, err := c.do(ctx, http.MethodDelete, path, nil)
	return err
}

// getPaged is get with Plex's own pagination headers set — a library
// section's full track list (AllTrackPaths) is the one response this
// client can't safely assume fits in a single page.
func (c *Client) getPaged(ctx context.Context, path string, query url.Values, start, size int) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Container-Start", strconv.Itoa(start))
	req.Header.Set("X-Plex-Container-Size", strconv.Itoa(size))
	return c.doRequest(req, path)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("Accept", "application/json")
	return c.doRequest(req, path)
}

func (c *Client) doRequest(req *http.Request, path string) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body for %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("plex: invalid server URL or token")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plex %s: status %d: %s", path, resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
