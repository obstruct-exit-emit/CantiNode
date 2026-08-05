// Package acervinode is a client for a self-hosted AcerviNode instance
// (https://github.com/obstruct-exit-emit/AcerviNode), CantiNode's download
// client — it speaks the same qBittorrent Web API and SABnzbd API shims
// AcerviNode itself exposes for Sonarr/Radarr, exactly as if CantiNode
// were another *arr app. See docs/qbittorrent-api.md and
// docs/sabnzbd-api.md in the AcerviNode repo for the exact contracts this
// implements against.
package acervinode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// musicCategory is the category CantiNode adds every download under.
// Deliberately "music" — AcerviNode pre-registers this exact category
// automatically on first run as Lidarr's own well-known default (see
// cmd/acervinode's defaultArrCategories), so it needs no separate setup
// step on the AcerviNode side before CantiNode can use it.
const musicCategory = "music"

// Client is an AcerviNode client, speaking both of its *arr-compat shims.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string // AcerviNode's own API key — see docs/configuration.md there

	sessionMu sync.Mutex
	sessionID string // qBittorrent shim's SID cookie value, empty until ensureSession succeeds
}

// NewClient returns a Client against a self-hosted AcerviNode at baseURL
// (e.g. "http://localhost:7846"), authenticating with apiKey — its own
// native API key, doubling as the qBittorrent shim's login password
// (see ensureSession) and the SABnzbd shim's apikey query parameter
// (see doSABnzbd).
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
	}
}

// ensureSession logs into the qBittorrent shim if this Client doesn't
// already hold a session cookie — see docs/qbittorrent-api.md: any
// username is accepted, only the password (AcerviNode's own API key)
// matters.
func (c *Client) ensureSession(ctx context.Context) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionID != "" {
		return nil
	}
	return c.loginLocked(ctx)
}

// loginLocked performs the actual login call — callers must hold
// sessionMu. Split out from ensureSession so doTorrent's 403-retry path
// (an expired session — AcerviNode's own sessions last 24h) can force a
// fresh login without the "already have one" short-circuit.
func (c *Client) loginLocked(ctx context.Context) error {
	form := url.Values{"username": {"cantinode"}, "password": {c.apiKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("acervinode: login request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("acervinode: read login response: %w", err)
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("acervinode: login rejected (wrong api_key?)")
	}

	for _, ck := range resp.Cookies() {
		if ck.Name == "SID" {
			c.sessionID = ck.Value
			return nil
		}
	}
	return fmt.Errorf("acervinode: login succeeded but no session cookie was returned")
}

// doTorrent performs an authenticated qBittorrent-shim request, logging
// in first if needed and retrying once (with a forced fresh login) on a
// 403 — AcerviNode's own sessions expire after 24h, and a long-running
// CantiNode process shouldn't need a restart just because one did.
func (c *Client) doTorrent(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}

	do := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		c.sessionMu.Lock()
		req.AddCookie(&http.Cookie{Name: "SID", Value: c.sessionID})
		c.sessionMu.Unlock()
		return c.httpClient.Do(req)
	}

	resp, err := do()
	if err != nil {
		return nil, fmt.Errorf("acervinode: %s %s: %w", method, path, err)
	}
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		c.sessionMu.Lock()
		c.sessionID = ""
		loginErr := c.loginLocked(ctx)
		c.sessionMu.Unlock()
		if loginErr != nil {
			return nil, loginErr
		}
		// body (if any) was an io.Reader, already consumed by the first
		// attempt — every doTorrent caller in this package passes either
		// nil or a *bytes.Reader/*bytes.Buffer it constructed itself and
		// can't rewind here, so retrying a body-bearing request isn't
		// supported. Every current caller with a body (addTorrentFile) is
		// fine with this in practice: a freshly started CantiNode won't
		// hit an expired session on its very first request.
		resp, err = do()
		if err != nil {
			return nil, fmt.Errorf("acervinode: %s %s (retry): %w", method, path, err)
		}
	}
	return resp, nil
}

// doSABnzbd performs a SABnzbd-shim request — apikey-per-request auth
// (see docs/sabnzbd-api.md), no session/login step at all.
func (c *Client) doSABnzbd(ctx context.Context, method string, form url.Values, body io.Reader, contentType string) (*http.Response, error) {
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
		return nil, fmt.Errorf("acervinode: sabnzbd request: %w", err)
	}
	return resp, nil
}
