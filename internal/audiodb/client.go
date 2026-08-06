// Package audiodb is a thin client for TheAudioDB
// (https://www.theaudiodb.com/api_guide.php), used to fetch an artist's
// biography and photo for CantiNode's unified artist page. Deliberately
// narrow — one lookup, by MusicBrainz artist ID — since that's the only
// thing internal/acquisition needs: bio/image are cached in the database
// on first monitor (or an explicit "Refresh metadata") and never fetched
// just from browsing, so this client is never on any request the web UI
// waits on directly.
package audiodb

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

const defaultBaseURL = "https://theaudiodb.com/api/v1/json"

// publicTestKey is TheAudioDB's well-documented shared API key for free,
// non-commercial, low-volume use (https://www.theaudiodb.com/api_guide.php)
// — the same key Lidarr itself uses for artist metadata, so this is an
// established, legitimate fallback rather than a hack. Used whenever an
// operator hasn't set their own key in Settings.
const publicTestKey = "2"

// minRequestInterval throttles requests gently — TheAudioDB has no
// published rate limit as strict as MusicBrainz's, but hammering a free
// third-party API is still bad manners.
const minRequestInterval = time.Second

// Client is a rate-limited TheAudioDB client. Safe for concurrent use —
// every request goes through the same throttle.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	userAgent   string
	minInterval time.Duration

	mu          sync.Mutex
	lastRequest time.Time
}

// NewClient returns a Client authenticated with apiKey, falling back to
// TheAudioDB's own publicTestKey if apiKey is empty (CantiNode's config
// treats audiodb_api_key as optional — see internal/config).
func NewClient(apiKey string) *Client {
	return NewClientWithBaseURL(apiKey, defaultBaseURL)
}

// NewClientWithBaseURL is NewClient against a non-default base URL — used
// by tests to point at an httptest.Server.
func NewClientWithBaseURL(apiKey, baseURL string) *Client {
	if apiKey == "" {
		apiKey = publicTestKey
	}
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    baseURL,
		apiKey:     apiKey,
		// No app version/contact email baked in here the way
		// internal/musicbrainz's User-Agent is — TheAudioDB's usage policy
		// doesn't require one, unlike MusicBrainz's — but a descriptive,
		// identifiable UA is still good citizenship against any free
		// third-party API.
		userAgent:   "CantiNode ( https://github.com/cantinode/cantinode )",
		minInterval: minRequestInterval,
	}
}

// ArtistMeta is the bio/image CantiNode caches for an artist — see
// database.Artist's Bio/ImageURL columns, populated from this via
// internal/acquisition.
type ArtistMeta struct {
	Bio      string
	ImageURL string
}

type artistLookupResponse struct {
	Artists []audioDBArtist `json:"artists"`
}

type audioDBArtist struct {
	BiographyEN  string `json:"strBiographyEN"`
	ArtistThumb  string `json:"strArtistThumb"`
	ArtistFanart string `json:"strArtistFanart"`
}

// LookupArtistByMBID fetches mbid's biography/image from TheAudioDB.
// Returns (nil, nil) — not an error — when TheAudioDB simply doesn't have
// this artist (a real, common case: MusicBrainz's catalog is far larger
// than TheAudioDB's), so callers (internal/acquisition's monitor/refresh
// flow) can degrade gracefully to no bio/photo rather than failing the
// whole operation over an artist TheAudioDB never indexed.
func (c *Client) LookupArtistByMBID(ctx context.Context, mbid string) (*ArtistMeta, error) {
	body, err := c.get(ctx, "/artist-mb.php", url.Values{"i": {mbid}})
	if err != nil {
		return nil, err
	}

	var resp artistLookupResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode artist %s: %w", mbid, err)
	}
	if len(resp.Artists) == 0 {
		return nil, nil
	}

	a := resp.Artists[0]
	imageURL := a.ArtistThumb
	if imageURL == "" {
		imageURL = a.ArtistFanart
	}
	return &ArtistMeta{Bio: a.BiographyEN, ImageURL: imageURL}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if err := c.throttle(ctx); err != nil {
		return nil, err
	}

	u := c.baseURL + "/" + c.apiKey + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body for %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audiodb %s: status %d: %s", path, resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// throttle blocks until at least minInterval has passed since the last
// request started, enforcing this client's own self-imposed rate limit
// across every call, however many goroutines are calling it — same
// pattern as internal/musicbrainz.Client.throttle.
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
