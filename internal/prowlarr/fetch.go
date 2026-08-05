package prowlarr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ContentKind is what FetchContent resolved a release's link to.
type ContentKind string

const (
	// KindMagnet means the release resolved to a magnet URI — nothing to
	// download, hand the URI itself straight to a torrent client.
	KindMagnet ContentKind = "magnet"
	// KindFile means Data holds the actual .torrent or .nzb file bytes.
	KindFile ContentKind = "file"
)

// FetchedContent is what FetchContent resolves a Release's link to.
type FetchedContent struct {
	Kind      ContentKind
	MagnetURI string // set when Kind == KindMagnet
	Data      []byte // set when Kind == KindFile
	Filename  string // set when Kind == KindFile — see Release.FileName
}

// FetchContent resolves rel's MagnetURL (preferred) or DownloadURL into
// either a real magnet URI or the actual downloaded file bytes — both
// fields are already Prowlarr-proxied links (see the package doc
// comment), so this talks to Prowlarr itself, not the indexer directly.
//
// Redirects are followed manually (up to maxRedirects hops) rather than
// via http.Client's own automatic following, specifically so a redirect
// straight to a magnet: URI (not a fetchable resource — there's nothing
// to GET) is recognized and returned as-is instead of the HTTP client
// erroring trying to dial an unsupported scheme.
func (c *Client) FetchContent(ctx context.Context, rel Release) (*FetchedContent, error) {
	target := rel.MagnetURL
	if target == "" {
		target = rel.DownloadURL
	}
	if target == "" {
		return nil, fmt.Errorf("prowlarr: release %q has neither magnetUrl nor downloadUrl", rel.Title)
	}

	const maxRedirects = 10
	for hop := 0; hop < maxRedirects; hop++ {
		if strings.HasPrefix(target, "magnet:") {
			return &FetchedContent{Kind: KindMagnet, MagnetURI: target}, nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.noRedirectHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", target, err)
		}

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return nil, fmt.Errorf("prowlarr: redirect from %s had no Location header", target)
			}
			resolved, err := resolveURL(target, loc)
			if err != nil {
				return nil, fmt.Errorf("resolve redirect location: %w", err)
			}
			target = resolved
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("prowlarr: fetch %s: status %d", target, resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read content from %s: %w", target, err)
		}
		return &FetchedContent{Kind: KindFile, Data: data, Filename: rel.FileName()}, nil
	}

	return nil, fmt.Errorf("prowlarr: too many redirects fetching %q", rel.Title)
}

// resolveURL resolves a Location header value (which may be relative)
// against the request URL it came from — the raw header string, not a
// re-serialized url.URL, so a magnet: URI's exact query encoding
// survives untouched.
func resolveURL(from, location string) (string, error) {
	if strings.HasPrefix(location, "magnet:") {
		return location, nil
	}
	base, err := url.Parse(from)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}
