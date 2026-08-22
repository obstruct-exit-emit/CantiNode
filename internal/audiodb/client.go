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
		userAgent:   "CantiNode ( https://github.com/obstruct-exit-emit/CantiNode )",
		minInterval: minRequestInterval,
	}
}

// ArtistMeta is what CantiNode uses from TheAudioDB for an artist:
// Bio/ImageURL cached to database.Artist's own Bio/ImageURL columns via
// internal/acquisition, and IDArtist — TheAudioDB's own internal numeric
// artist id, not the MBID — for linking out to the artist's own page on
// theaudiodb.com, the same non-MBID-based URL scheme AlbumMeta.IDAlbum
// exists for.
type ArtistMeta struct {
	Bio      string
	ImageURL string
	IDArtist string
}

type artistLookupResponse struct {
	Artists []audioDBArtist `json:"artists"`
}

type audioDBArtist struct {
	// Biography is TheAudioDB's English artist biography — the field is
	// genuinely named "strBiography" with no language suffix (other
	// languages get one: strBiographyDE, strBiographyFR, ...); there is no
	// "strBiographyEN" in TheAudioDB's actual schema at all, so a struct
	// tag of that name silently decoded to an empty string for every
	// artist, always, regardless of whether TheAudioDB really had a bio.
	Biography    string `json:"strBiography"`
	ArtistThumb  string `json:"strArtistThumb"`
	ArtistFanart string `json:"strArtistFanart"`
	IDArtist     string `json:"idArtist"`
}

// LookupArtistByMBID fetches mbid's biography/image/id from TheAudioDB.
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
	return &ArtistMeta{Bio: a.Biography, ImageURL: imageURL, IDArtist: a.IDArtist}, nil
}

// AlbumMeta is what CantiNode uses from TheAudioDB for a release group:
// ThumbURL for cover art (see internal/coverart, which tries this first
// and falls back to Cover Art Archive when TheAudioDB doesn't have it),
// IDAlbum — TheAudioDB's own internal numeric album id, not the MBID —
// for linking out to the album's own page on theaudiodb.com (which, unlike
// MusicBrainz, doesn't use MBIDs in its browsable URLs at all), Description,
// the album's own write-up, and Mood (e.g. "Trippy", "Melancholic") — both
// TheAudioDB's own fields, with no MusicBrainz equivalent at all.
type AlbumMeta struct {
	ThumbURL    string
	IDAlbum     string
	Description string
	Mood        string
}

type albumLookupResponse struct {
	// Album is null (not an empty array) when TheAudioDB has nothing for
	// this id — encoding/json decodes a JSON null into a nil slice either
	// way, so no special-casing needed beyond the existing len(...) == 0
	// check LookupArtistByMBID already uses.
	Album []audioDBAlbum `json:"album"`
}

type audioDBAlbum struct {
	AlbumThumb  string `json:"strAlbumThumb"`
	IDAlbum     string `json:"idAlbum"`
	Description string `json:"strDescription"`
	Mood        string `json:"strMood"`
}

// LookupAlbumByReleaseGroupMBID fetches releaseGroupMBID's own entry from
// TheAudioDB — keyed by release GROUP (the "album" as a whole), not a
// specific release/edition, which is the granularity TheAudioDB's own
// schema uses (confirmed live: /album-mb.php?i= takes a release-group
// MBID and returns strMusicBrainzID matching it back). Returns (nil, nil)
// — not an error — when TheAudioDB has nothing for it at all, same
// convention as LookupArtistByMBID, so internal/coverart can fall back to
// Cover Art Archive without treating a miss as a failure. A returned
// AlbumMeta's own ThumbURL/IDAlbum may individually still be empty (an
// entry can exist with one populated and not the other) — callers check
// whichever field they need.
func (c *Client) LookupAlbumByReleaseGroupMBID(ctx context.Context, releaseGroupMBID string) (*AlbumMeta, error) {
	body, err := c.get(ctx, "/album-mb.php", url.Values{"i": {releaseGroupMBID}})
	if err != nil {
		return nil, err
	}

	var resp albumLookupResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode album %s: %w", releaseGroupMBID, err)
	}
	if len(resp.Album) == 0 {
		return nil, nil
	}
	return &AlbumMeta{
		ThumbURL:    resp.Album[0].AlbumThumb,
		IDAlbum:     resp.Album[0].IDAlbum,
		Description: resp.Album[0].Description,
		Mood:        resp.Album[0].Mood,
	}, nil
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
