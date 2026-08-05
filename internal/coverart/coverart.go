// Package coverart fetches an album's front cover from the Cover Art
// Archive (https://coverartarchive.org), keyed by the release MBID
// already stored on a matched database.Album, and caches it to disk so
// the same release is never re-fetched.
package coverart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrNoCoverArt means the Cover Art Archive authoritatively has no front
// cover for this release (a real 404, not a transient failure) — cached
// to disk as a sentinel file so it isn't re-fetched on every request,
// unlike a network error, which isn't cached at all so the next request
// simply tries again.
var ErrNoCoverArt = errors.New("coverart: no cover art for this release")

// noCoverSentinelExt marks a release Cover Art Archive has confirmed has
// no front cover — an empty file, its extension is the whole signal.
const noCoverSentinelExt = ".nocover"

const defaultBaseURL = "https://coverartarchive.org"

// Client fetches and disk-caches front cover images.
type Client struct {
	httpClient *http.Client
	userAgent  string
	cacheDir   string
	baseURL    string
}

// NewClient returns a Client caching images under cacheDir (created if
// necessary on first use, not here) and identifying itself with
// userAgent — Cover Art Archive is hosted alongside MusicBrainz's own
// infrastructure, so the same courtesy of a descriptive User-Agent
// applies (see internal/musicbrainz.NewClient).
func NewClient(cacheDir, userAgent string) *Client {
	return NewClientWithBaseURL(cacheDir, userAgent, defaultBaseURL)
}

// NewClientWithBaseURL is NewClient against a non-default Cover Art
// Archive-compatible server — mainly a test seam (internal/api's own
// tests use it to stand up a fake server), but also there for the same
// "self-hosted mirror" reasoning as
// musicbrainz.NewClientWithBaseURL.
func NewClientWithBaseURL(cacheDir, userAgent, baseURL string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		userAgent:  userAgent,
		cacheDir:   cacheDir,
		baseURL:    baseURL,
	}
}

// GetFrontCover returns the local filesystem path and content type of
// releaseMBID's front cover, fetching and caching it from Cover Art
// Archive on the first request for it. Returns ErrNoCoverArt if the
// release has no cover art there (also cached, so this doesn't hit the
// network again for the same release).
func (c *Client) GetFrontCover(ctx context.Context, releaseMBID string) (path string, contentType string, err error) {
	if releaseMBID == "" {
		return "", "", fmt.Errorf("coverart: releaseMBID must not be empty")
	}

	if cached, ct, ok := c.checkCache(releaseMBID); ok {
		return cached, ct, nil
	}
	if c.hasNoCoverSentinel(releaseMBID) {
		return "", "", ErrNoCoverArt
	}

	return c.fetchAndCache(ctx, releaseMBID)
}

func (c *Client) checkCache(releaseMBID string) (path, contentType string, ok bool) {
	for ext, ct := range extToContentType {
		p := filepath.Join(c.cacheDir, releaseMBID+ext)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, ct, true
		}
	}
	return "", "", false
}

func (c *Client) hasNoCoverSentinel(releaseMBID string) bool {
	_, err := os.Stat(filepath.Join(c.cacheDir, releaseMBID+noCoverSentinelExt))
	return err == nil
}

var extToContentType = map[string]string{
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

var contentTypeToExt = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func (c *Client) fetchAndCache(ctx context.Context, releaseMBID string) (path, contentType string, err error) {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cover art cache dir: %w", err)
	}

	// The "-250" thumbnail, not the full-size image — CantiNode only ever
	// displays this at library-grid thumbnail size, and a release's
	// original scan can be tens of megabytes.
	url := fmt.Sprintf("%s/release/%s/front-250", c.baseURL, releaseMBID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch cover art: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		sentinel := filepath.Join(c.cacheDir, releaseMBID+noCoverSentinelExt)
		if werr := os.WriteFile(sentinel, nil, 0o644); werr != nil {
			return "", "", fmt.Errorf("write no-cover sentinel: %w", werr)
		}
		return "", "", ErrNoCoverArt
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("coverart: unexpected status %d for %s", resp.StatusCode, url)
	}

	ct := resp.Header.Get("Content-Type")
	ext, ok := contentTypeToExt[ct]
	if !ok {
		ext = ".jpg" // Cover Art Archive serves jpeg in the overwhelming majority of cases
		ct = "image/jpeg"
	}

	dest := filepath.Join(c.cacheDir, releaseMBID+ext)
	tmp, err := os.CreateTemp(c.cacheDir, ".cantinode-cover-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("write cover art: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return "", "", fmt.Errorf("cache cover art: %w", err)
	}

	return dest, ct, nil
}
