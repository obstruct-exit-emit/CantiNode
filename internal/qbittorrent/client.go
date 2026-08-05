// Package qbittorrent is a client for the real qBittorrent Web API
// (https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API) — CantiNode's
// torrent-side download client. Deliberately generic, not tied to any one
// server: it works against a genuine qBittorrent instance, or against
// AcerviNode (https://github.com/obstruct-exit-emit/AcerviNode), which
// exposes the same API subset specifically so that *arr-shaped apps (this
// one included) can talk to it without special-casing — see
// docs/qbittorrent-api.md in the AcerviNode repo for exactly which surface
// it implements.
package qbittorrent

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
// Deliberately "music" — Lidarr's own well-known default category name
// (see e.g. AcerviNode's cmd/acervinode/defaultArrCategories, which
// pre-registers it automatically), so a fresh setup needs no separate
// category step. A real qBittorrent instance registers a category the
// first time it's used the same way createCategory does, so this needs
// no pre-configuration there either.
const musicCategory = "music"

// Client is a qBittorrent Web API client.
type Client struct {
	httpClient *http.Client
	baseURL    string
	username   string
	password   string

	sessionMu sync.Mutex
	sessionID string // the SID cookie value, empty until ensureSession succeeds
}

// NewClient returns a Client against a qBittorrent-Web-API-compatible
// server at baseURL (e.g. "http://localhost:8080" for a real qBittorrent,
// or an AcerviNode instance's own port) authenticating with username/
// password — a real qBittorrent checks both; AcerviNode's own compat shim
// accepts any username and checks password against its own API key (see
// docs/qbittorrent-api.md there).
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		username:   username,
		password:   password,
	}
}

// ensureSession logs in if this Client doesn't already hold a session
// cookie.
func (c *Client) ensureSession(ctx context.Context) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionID != "" {
		return nil
	}
	return c.loginLocked(ctx)
}

// loginLocked performs the actual login call — callers must hold
// sessionMu. Split out from ensureSession so do's 403-retry path (an
// expired session) can force a fresh login without the "already have
// one" short-circuit.
func (c *Client) loginLocked(ctx context.Context) error {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent: login request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("qbittorrent: read login response: %w", err)
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("qbittorrent: login rejected (check username/password)")
	}

	for _, ck := range resp.Cookies() {
		if ck.Name == "SID" {
			c.sessionID = ck.Value
			return nil
		}
	}
	return fmt.Errorf("qbittorrent: login succeeded but no session cookie was returned")
}

// do performs an authenticated request, logging in first if needed and
// retrying once (with a forced fresh login) on a 403 — a session can
// expire server-side (real qBittorrent's own default is 60 minutes of
// inactivity; AcerviNode's is 24h) and a long-running CantiNode process
// shouldn't need a restart just because one did.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, err
	}

	attempt := func() (*http.Response, error) {
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

	resp, err := attempt()
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: %s %s: %w", method, path, err)
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
		// attempt — every caller with a body (AddTorrentFile) constructs
		// its own *bytes.Reader/*bytes.Buffer that can't be rewound here,
		// so retrying a body-bearing request isn't supported. Fine in
		// practice: a freshly started CantiNode won't hit an expired
		// session on its very first request.
		resp, err = attempt()
		if err != nil {
			return nil, fmt.Errorf("qbittorrent: %s %s (retry): %w", method, path, err)
		}
	}
	return resp, nil
}
