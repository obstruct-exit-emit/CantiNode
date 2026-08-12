// Package musicbrainz is a thin client for the MusicBrainz web service
// (https://musicbrainz.org/doc/MusicBrainz_API), used to match a scanned
// audio file to a canonical artist/release/recording and to fetch the
// metadata CantiNode's library is built from.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://musicbrainz.org/ws/2"

// minRequestInterval enforces MusicBrainz's rate-limiting policy
// (https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting): at most one
// request per second per client. Set slightly above 1s, not exactly 1s, to
// absorb clock/scheduling jitter rather than shave requests right up
// against the limit.
const minRequestInterval = 1100 * time.Millisecond

// retryBaseDelay is the first backoff a retried request waits, doubling
// each further attempt (see Client.get) — separate from minInterval, which
// paces every request (successful or not) regardless of retries.
const retryBaseDelay = 500 * time.Millisecond

// Client is a rate-limited MusicBrainz web service client. Safe for
// concurrent use — every request goes through the same throttle.
type Client struct {
	httpClient     *http.Client
	baseURL        string
	userAgent      string
	minInterval    time.Duration
	retryBaseDelay time.Duration

	mu          sync.Mutex
	lastRequest time.Time
}

// NewClient returns a Client identifying itself with a User-Agent built
// from appVersion and contactEmail, as MusicBrainz's usage policy requires
// (https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) — a
// well-formed User-Agent with no real contact info is still commonly
// flagged. contactEmail may be empty (CantiNode's config.yaml treats it as
// optional), producing a User-Agent that's still descriptive, just without
// a way for MusicBrainz to reach the operator.
func NewClient(appVersion, contactEmail string) *Client {
	return NewClientWithBaseURL(appVersion, contactEmail, defaultBaseURL)
}

// NewClientWithBaseURL is NewClient against a non-default MusicBrainz-API-
// compatible server — a self-hosted mirror, for an operator who'd rather
// not send every scanned filename to musicbrainz.org, or (the same knob)
// a test server.
func NewClientWithBaseURL(appVersion, contactEmail, baseURL string) *Client {
	ua := fmt.Sprintf("CantiNode/%s ( https://github.com/obstruct-exit-emit/CantiNode )", appVersion)
	if contactEmail != "" {
		ua = fmt.Sprintf("CantiNode/%s ( %s )", appVersion, contactEmail)
	}
	return &Client{
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		baseURL:        baseURL,
		userAgent:      ua,
		minInterval:    minRequestInterval,
		retryBaseDelay: retryBaseDelay,
	}
}

// LookupRecording fetches a single recording by MBID, with its artist
// credit and associated releases — used when a scanned file's own tags
// already carry a MusicBrainz recording ID (the high-confidence,
// direct-match path in internal/scanner).
func (c *Client) LookupRecording(ctx context.Context, mbid string) (*Recording, error) {
	body, err := c.get(ctx, "/recording/"+url.PathEscape(mbid), url.Values{
		"inc": {"artist-credits+releases+release-groups"},
		"fmt": {"json"},
	})
	if err != nil {
		return nil, err
	}
	var rec Recording
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("decode recording %s: %w", mbid, err)
	}
	return &rec, nil
}

// LookupArtist fetches a single artist by MBID, with their full release
// group list plus genres/tags/rating — used by internal/acquisition to
// seed a newly monitored artist's wanted albums, and by
// internal/api.refreshMusicArtistMetadata to cache everything about the
// artist worth keeping, even fields nothing displays yet.
func (c *Client) LookupArtist(ctx context.Context, mbid string) (*Artist, error) {
	body, err := c.get(ctx, "/artist/"+url.PathEscape(mbid), url.Values{
		"inc": {"release-groups+genres+tags+ratings"},
		"fmt": {"json"},
	})
	if err != nil {
		return nil, err
	}
	var artist Artist
	if err := json.Unmarshal(body, &artist); err != nil {
		return nil, fmt.Errorf("decode artist %s: %w", mbid, err)
	}
	return &artist, nil
}

// SearchArtists fuzzy-searches for artists matching name, ordered by
// MusicBrainz's own relevance score, most relevant first — used by the
// "monitor an artist" UI to resolve a plain-text name to an MBID.
func (c *Client) SearchArtists(ctx context.Context, name string) ([]Artist, error) {
	if name == "" {
		return nil, fmt.Errorf("search artists: name must not be empty")
	}
	body, err := c.get(ctx, "/artist/", url.Values{
		"query": {`artist:"` + escapeQuoted(name) + `"`},
		"fmt":   {"json"},
		"limit": {"10"},
	})
	if err != nil {
		return nil, err
	}
	var resp artistSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode artist search: %w", err)
	}
	return resp.Artists, nil
}

// SearchRecordings fuzzy-searches for recordings matching artist/release/
// title (any may be empty, but at least one should be set for a useful
// result), ordered by MusicBrainz's own relevance score (Recording.Score,
// 0-100), most relevant first. Used when a scanned file has no MusicBrainz
// ID of its own to look up directly.
//
// release is sanitized first (see sanitizeReleaseTitle) — a raw folder/
// tag value like "... SHM-CD" or "... 24-96 hdtracks" (common in files
// sourced from torrents/usenet, including CantiNode's own acquisition
// pipeline) searches far worse than the same title with that rip/format
// noise removed, since real MusicBrainz release titles never carry it.
func (c *Client) SearchRecordings(ctx context.Context, artist, release, title string) ([]Recording, error) {
	query := buildRecordingQuery(artist, sanitizeReleaseTitle(release), title)
	if query == "" {
		return nil, fmt.Errorf("search recordings: at least one of artist, release, title must be non-empty")
	}

	body, err := c.get(ctx, "/recording/", url.Values{
		"query": {query},
		"fmt":   {"json"},
		"limit": {"5"},
	})
	if err != nil {
		return nil, err
	}
	var resp recordingSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode recording search: %w", err)
	}
	return resp.Recordings, nil
}

// SearchReleases fuzzy-searches for releases (albums) matching artist/
// release title, ordered by MusicBrainz's own relevance score, most
// relevant first — used by internal/scanner's folder-level matching to
// find the one MusicBrainz release a folder of files most likely
// represents, when no file in the folder already carries an embedded
// release MBID of its own.
//
// release is sanitized the same way SearchRecordings sanitizes it (see
// sanitizeReleaseTitle) — same rip/format-junk problem, same fix.
func (c *Client) SearchReleases(ctx context.Context, artist, release string) ([]ReleaseSearchResult, error) {
	query := buildReleaseQuery(artist, sanitizeReleaseTitle(release))
	if query == "" {
		return nil, fmt.Errorf("search releases: at least one of artist, release must be non-empty")
	}

	body, err := c.get(ctx, "/release/", url.Values{
		"query": {query},
		"fmt":   {"json"},
		"limit": {"10"},
	})
	if err != nil {
		return nil, err
	}
	var resp releaseSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode release search: %w", err)
	}
	return resp.Releases, nil
}

// BrowseReleaseGroupReleases lists every release (version/edition)
// belonging to release group releaseGroupMBID — a MusicBrainz "browse"
// request (filtered by relation, not full-text relevance), used both to
// preview an album's tracklist before CantiNode owns any file of it (the
// Missing/Wanted sections have a release group from an artist's cached
// discography, but no scanned file to resolve a specific release from the
// way folder-level matching does) and to populate a release-version picker
// (see internal/api's cacheReleaseGroupVersions). inc=media adds each
// release's disc/format breakdown (ReleaseSearchResult.Media) without the
// cost of a full per-track fetch — enough to tell editions apart (a
// single-disc reissue vs. the original 2×CD release) and to score a
// version against a folder's own file count.
func (c *Client) BrowseReleaseGroupReleases(ctx context.Context, releaseGroupMBID string) ([]ReleaseSearchResult, error) {
	body, err := c.get(ctx, "/release/", url.Values{
		"release-group": {releaseGroupMBID},
		"inc":           {"media"},
		"fmt":           {"json"},
		"limit":         {"100"},
	})
	if err != nil {
		return nil, err
	}
	var resp releaseSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode release-group %s releases: %w", releaseGroupMBID, err)
	}
	return resp.Releases, nil
}

// buildReleaseQuery mirrors buildRecordingQuery's scoping, just without a
// title field — a release search has no per-track title to scope by.
func buildReleaseQuery(artist, release string) string {
	var parts []string
	if release != "" {
		parts = append(parts, `release:"`+escapeQuoted(release)+`"`)
	}
	if artist != "" {
		parts = append(parts, `artist:"`+escapeQuoted(artist)+`"`)
	}
	return strings.Join(parts, " AND ")
}

// LookupReleaseWithTracklist fetches a single release by MBID, with its
// full medium/track breakdown — used once a target release has been
// chosen (either a file's own embedded release MBID, or the top
// SearchReleases candidate) to slot every local file in a folder into a
// specific track position within that one release.
func (c *Client) LookupReleaseWithTracklist(ctx context.Context, mbid string) (*ReleaseWithTracklist, error) {
	body, err := c.get(ctx, "/release/"+url.PathEscape(mbid), url.Values{
		"inc": {"recordings+artist-credits+release-groups"},
		"fmt": {"json"},
	})
	if err != nil {
		return nil, err
	}
	var rel ReleaseWithTracklist
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("decode release %s: %w", mbid, err)
	}
	return &rel, nil
}

// buildRecordingQuery builds a MusicBrainz (Lucene) search query scoped to
// each non-empty field, so a search for a specific artist doesn't get
// diluted by an unrelated recording that merely mentions the track title.
func buildRecordingQuery(artist, release, title string) string {
	var parts []string
	if title != "" {
		parts = append(parts, `recording:"`+escapeQuoted(title)+`"`)
	}
	if artist != "" {
		parts = append(parts, `artist:"`+escapeQuoted(artist)+`"`)
	}
	if release != "" {
		parts = append(parts, `release:"`+escapeQuoted(release)+`"`)
	}
	return strings.Join(parts, " AND ")
}

// escapeQuoted escapes the two characters that are still special inside a
// double-quoted Lucene phrase (the backslash itself, and the quote that
// would otherwise end the phrase early).
func escapeQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// maxRetries bounds how many times get retries a transient server-side
// error (see retryable) before giving up — 2 retries (3 attempts total).
const maxRetries = 2

// get issues one MusicBrainz request, retrying a transient server-side
// error (503 "busy", 429, 502, 504 — all observed from the public
// musicbrainz.org server under normal load, not a sign anything is
// actually wrong) with a short backoff before surfacing it, instead of
// bubbling the very first blip straight up as a hard error a user has to
// notice and manually retry themselves.
func (c *Client) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path + "?" + query.Encode()

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.retryBaseDelay << (attempt - 1) // e.g. 500ms, 1s
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := c.throttle(ctx); err != nil {
			return nil, err
		}

		body, status, err := c.doGet(ctx, u, path)
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			return body, nil
		}
		lastErr = fmt.Errorf("musicbrainz %s: status %d: %s", path, status, truncate(string(body), 300))
		if !retryableStatus(status) {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

// retryableStatus reports whether a MusicBrainz response status is a
// transient server-side condition worth retrying, rather than something a
// retry can't fix (a bad request, an unknown MBID, an auth problem).
func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (c *Client) doGet(ctx context.Context, u, path string) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response body for %s: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// throttle blocks until at least minInterval has passed since the last
// request started, enforcing MusicBrainz's 1 req/sec policy across every
// call this Client makes, however many goroutines are calling it.
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
