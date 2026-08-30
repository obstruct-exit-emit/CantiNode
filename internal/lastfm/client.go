// Package lastfm is a thin client for Last.fm's public web API
// (https://www.last.fm/api), used by internal/importlist to resolve a
// user's or a tag's top artists into a list of artist names/MBIDs to add
// and monitor. Deliberately narrow — the two "top artists" endpoints only —
// since that's all the Last.fm import-list source type needs.
package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultBaseURL = "https://ws.audioscrobbler.com/2.0/"

// minRequestInterval throttles requests gently — Last.fm's own published
// limit is roughly 5 req/sec per API key, well above what a periodic
// import-list sync ever needs, but a self-imposed floor is still good
// manners against a free third-party API (same reasoning as
// internal/audiodb.minRequestInterval).
const minRequestInterval = 250 * time.Millisecond

// Client is a rate-limited Last.fm client. Safe for concurrent use — every
// request goes through the same throttle.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	minInterval time.Duration

	mu          sync.Mutex
	lastRequest time.Time
}

// NewClient returns a Client authenticated with apiKey. Unlike
// internal/audiodb, Last.fm has no shared public test key an unconfigured
// operator can fall back to — an empty apiKey is a real "not configured"
// state callers (internal/importlist) must check for and refuse outright
// rather than silently trying an empty-string request that would just
// 403 from Last.fm anyway.
func NewClient(apiKey string) *Client {
	return NewClientWithBaseURL(apiKey, defaultBaseURL)
}

// NewClientWithBaseURL is NewClient against a non-default base URL — used
// by tests to point at an httptest.Server.
func NewClientWithBaseURL(apiKey, baseURL string) *Client {
	return &Client{
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		baseURL:     baseURL,
		apiKey:      apiKey,
		minInterval: minRequestInterval,
	}
}

// TopArtist is one entry of a "top artists" response. MBID is often empty —
// Last.fm's own catalog links many artists to MusicBrainz, but far from
// all — callers needing a firm MBID fall back to a MusicBrainz name search
// when it's blank.
type TopArtist struct {
	Name string
	MBID string
}

type topArtistsResponse struct {
	TopArtists struct {
		Artist []struct {
			Name string `json:"name"`
			MBID string `json:"mbid"`
		} `json:"artist"`
	} `json:"topartists"`
	// Error/Message are populated instead of TopArtists on failure (e.g.
	// invalid API key, unknown user) — Last.fm returns these with a 200
	// status, so the only way to detect failure is checking Error != 0.
	Error   int    `json:"error"`
	Message string `json:"message"`
}

// TopArtistsForUser fetches username's own top artists (user.gettopartists),
// most-played first, capped at limit.
func (c *Client) TopArtistsForUser(ctx context.Context, username string, limit int) ([]TopArtist, error) {
	return c.topArtists(ctx, url.Values{
		"method": {"user.gettopartists"},
		"user":   {username},
	}, limit)
}

// TopArtistsForTag fetches tag's own top artists (tag.gettopartists) —
// e.g. a genre name like "shoegaze" — most-associated first, capped at
// limit.
func (c *Client) TopArtistsForTag(ctx context.Context, tag string, limit int) ([]TopArtist, error) {
	return c.topArtists(ctx, url.Values{
		"method": {"tag.gettopartists"},
		"tag":    {tag},
	}, limit)
}

func (c *Client) topArtists(ctx context.Context, query url.Values, limit int) ([]TopArtist, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("lastfm: no API key configured")
	}
	if limit <= 0 {
		limit = 50
	}
	query.Set("api_key", c.apiKey)
	query.Set("format", "json")
	query.Set("limit", fmt.Sprint(limit))

	body, err := c.get(ctx, query)
	if err != nil {
		return nil, err
	}
	var resp topArtistsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode top artists: %w", err)
	}
	if resp.Error != 0 {
		return nil, fmt.Errorf("lastfm: %s", resp.Message)
	}
	out := make([]TopArtist, 0, len(resp.TopArtists.Artist))
	for _, a := range resp.TopArtists.Artist {
		out = append(out, TopArtist{Name: a.Name, MBID: a.MBID})
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, query url.Values) ([]byte, error) {
	if err := c.throttle(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "CantiNode ( https://github.com/obstruct-exit-emit/CantiNode )")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	// Deliberately not gated on resp.StatusCode == 200: Last.fm returns an
	// ordinary error JSON body (see topArtistsResponse.Error/Message) with
	// a non-200 status for most API-level failures (bad key, unknown
	// user/tag) — decoding first and checking Error lets one code path
	// surface a real message instead of a bare status code.
	return body, nil
}

// throttle blocks until at least minRequestInterval has passed since the
// last request started — same pattern as internal/musicbrainz.Client's and
// internal/audiodb.Client's own throttle.
func (c *Client) throttle(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	wait := c.minInterval - time.Since(c.lastRequest)
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.lastRequest = time.Now()
	return nil
}
