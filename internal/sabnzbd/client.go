// Package sabnzbd is a client for the real SABnzbd API
// (https://sabnzbd.org/wiki/advanced/api) — CantiNode's usenet-side
// download client. Deliberately generic, not tied to any one server: it
// works against a genuine SABnzbd instance, or against AcerviNode
// (https://github.com/obstruct-exit-emit/AcerviNode), which exposes the
// same API subset specifically so that *arr-shaped apps (this one
// included) can talk to it without special-casing — see
// docs/sabnzbd-api.md in the AcerviNode repo for exactly which surface it
// implements.
package sabnzbd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// musicCategory is the category CantiNode adds every download under —
// see internal/qbittorrent's identical constant for why "music"
// specifically. Unlike qBittorrent, **real SABnzbd has no API to create a
// category on the fly** (confirmed against AcerviNode's own
// docs/sabnzbd-api.md, which documents this as a genuine real-SABnzbd
// limitation, not just its own shim's) — against a real SABnzbd server,
// this category needs to be pre-created by hand once, in SABnzbd's own
// admin UI, before a grab will work. AcerviNode's own compat shim doesn't
// have this limitation (it auto-registers "music" on first run as
// Lidarr's well-known default).
const musicCategory = "music"

// Client is a SABnzbd API client.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient returns a Client against a SABnzbd-API-compatible server at
// baseURL (e.g. "http://localhost:8085" for a real SABnzbd, or an
// AcerviNode instance's own port), authenticating with apiKey — checked
// as a query parameter on every request, matching real SABnzbd's own
// auth model exactly (no login/session step at all).
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
	}
}

// do performs a SABnzbd API request — apikey-per-request auth, no
// session/login step at all.
func (c *Client) do(ctx context.Context, method string, form url.Values, body io.Reader, contentType string) (*http.Response, error) {
	form.Set("apikey", c.apiKey)
	u := c.baseURL + "/api?" + form.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: request: %w", err)
	}
	return resp, nil
}
