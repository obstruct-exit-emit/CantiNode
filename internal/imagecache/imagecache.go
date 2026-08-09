// Package imagecache downloads and stores remote provider images — author,
// series, and book/volume art from Hardcover/AniList/ComicVine — under the
// data directory, so the UI serves them from LibriNode instead of the
// provider CDN on every view, and they survive the provider's URL rot.
package imagecache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxImageSize caps a single download (provider covers are well under this).
const maxImageSize = 20 << 20

// fetchTimeout bounds one download.
const fetchTimeout = 30 * time.Second

type Cache struct {
	dir   string
	httpc *http.Client
}

func New(dir string) *Cache {
	return &Cache{dir: dir, httpc: &http.Client{Timeout: fetchTimeout}}
}

func key(url string) string {
	sum := sha1.Sum([]byte(url))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) path(url string) string { return filepath.Join(c.dir, key(url)) }

// cachedPath returns the stored path and whether a non-empty file exists.
func (c *Cache) cachedPath(url string) (string, bool) {
	p := c.path(url)
	if info, err := os.Stat(p); err == nil && info.Size() > 0 {
		return p, true
	}
	return p, false
}

// Read returns the cached bytes and detected content type for a URL, or false.
func (c *Cache) Read(url string) ([]byte, string, bool) {
	p, ok := c.cachedPath(url)
	if !ok {
		return nil, "", false
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	return data, http.DetectContentType(data), true
}

// Fetch returns the image for url — from cache when present, otherwise
// downloading it (and storing it) first. Only http/https image responses are
// accepted.
func (c *Cache) Fetch(ctx context.Context, url string) ([]byte, string, error) {
	if data, ct, ok := c.Read(url); ok {
		return data, ct, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, "", fmt.Errorf("imagecache: unsupported url scheme")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("imagecache: %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSize))
	if err != nil {
		return nil, "", err
	}
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", fmt.Errorf("imagecache: %s is not an image (%s)", url, ct)
	}
	c.store(url, data)
	return data, ct, nil
}

// Prefetch downloads url in the background, best-effort — the download-on-add
// path. A no-op when already cached or the url is empty.
func (c *Cache) Prefetch(url string) {
	if c == nil || url == "" {
		return
	}
	if _, ok := c.cachedPath(url); ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		if _, _, err := c.Fetch(ctx, url); err != nil {
			slog.Debug("image prefetch failed", "url", url, "error", err)
		}
	}()
}

// Clear removes every cached image, returning how many files were deleted
// and the bytes freed. The cache rebuilds on demand, so this is always safe.
func (c *Cache) Clear() (removed int, freed int64, err error) {
	entries, err := os.ReadDir(c.dir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			freed += info.Size()
		}
		if err := os.Remove(filepath.Join(c.dir, e.Name())); err == nil {
			removed++
		}
	}
	return removed, freed, nil
}

// store writes data atomically (temp file + rename) so a concurrent reader
// never sees a partial file. Failures are non-fatal.
func (c *Cache) store(url string, data []byte) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		slog.Debug("image cache mkdir failed", "error", err)
		return
	}
	tmp, err := os.CreateTemp(c.dir, ".img-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), c.path(url)); err != nil {
		os.Remove(tmp.Name())
	}
}
