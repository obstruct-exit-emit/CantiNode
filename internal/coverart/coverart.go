// Package coverart fetches an album's front cover, preferring TheAudioDB
// (keyed by release-group MBID) and falling back to the Cover Art Archive
// (https://coverartarchive.org, keyed by the specific release MBID
// already stored on a matched database.Album) whenever TheAudioDB doesn't
// have it — TheAudioDB's own catalog is far smaller than MusicBrainz's, so
// this only ever adds coverage, never removes it. Every image is cached to
// disk (under the release MBID's identity, regardless of which upstream
// source actually provided it) so the same release is never re-fetched.
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

	"github.com/cantinode/cantinode/internal/audiodb"
)

// ErrNoCoverArt means the Cover Art Archive authoritatively has no front
// cover for this release (a real 404, not a transient failure) — cached
// to disk as a sentinel file so it isn't re-fetched on every request,
// unlike a network error, which isn't cached at all so the next request
// simply tries again.
var ErrNoCoverArt = errors.New("coverart: no cover art for this release")

// noCoverSentinelExt marks a release Cover Art Archive has confirmed has
// no front cover — an empty file, its extension is the whole signal (its
// mtime is the other one — see noCoverRecheckAfter).
const noCoverSentinelExt = ".nocover"

// noAudioDBSentinelExt marks a release GROUP (TheAudioDB doesn't
// distinguish specific editions, so this is keyed by releaseGroupMBID, not
// releaseMBID like noCoverSentinelExt) TheAudioDB has confirmed has no
// album thumb — kept as its own file, separate from noCoverSentinelExt,
// specifically so a release cached as "Cover Art Archive has nothing"
// *before* TheAudioDB support existed (or before TheAudioDB itself gained
// this release) doesn't also silently mean "TheAudioDB has nothing" —
// found live: two Blind Melon albums stayed permanently cover-less because
// GetFrontCover's old single-sentinel design let an old Cover Art Archive
// 404 skip trying TheAudioDB at all, even though TheAudioDB genuinely had
// both. See GetFrontCover's own doc comment for the two sentinels' full
// interaction.
const noAudioDBSentinelExt = ".noaudiodb"

// noCoverRecheckAfter bounds how long a "no cover art" sentinel — either
// kind, noCoverSentinelExt or noAudioDBSentinelExt — is trusted before
// GetFrontCover tries that source again live — found live: neither Cover
// Art Archive's nor TheAudioDB's catalog is static, community members add
// art to a release after the fact (and TheAudioDB's own catalog keeps
// growing), so a real miss from months ago isn't a permanent answer the
// way, say, "this MBID doesn't exist" would be. Deliberately not zero
// (that would mean never caching a miss at all, hammering both providers
// on every single page view for a release that stays genuinely uncovered)
// and not too short (30 days keeps this a rare, not routine, re-check).
const noCoverRecheckAfter = 30 * 24 * time.Hour

const defaultBaseURL = "https://coverartarchive.org"

// Client fetches and disk-caches front cover images.
type Client struct {
	httpClient *http.Client
	userAgent  string
	cacheDir   string
	baseURL    string
	// audiodb is consulted first, by release-group MBID, before falling
	// back to Cover Art Archive — nil-able (tests, and any future startup
	// path that doesn't want it) skips straight to Cover Art Archive, the
	// same as TheAudioDB simply not having an entry.
	audiodb *audiodb.Client
}

// NewClient returns a Client caching images under cacheDir (created if
// necessary on first use, not here) and identifying itself with
// userAgent — Cover Art Archive is hosted alongside MusicBrainz's own
// infrastructure, so the same courtesy of a descriptive User-Agent
// applies (see internal/musicbrainz.NewClient). audiodbClient may be nil
// to skip TheAudioDB entirely and only ever use Cover Art Archive.
func NewClient(cacheDir, userAgent string, audiodbClient *audiodb.Client) *Client {
	return NewClientWithBaseURL(cacheDir, userAgent, defaultBaseURL, audiodbClient)
}

// NewClientWithBaseURL is NewClient against a non-default Cover Art
// Archive-compatible server — mainly a test seam (internal/api's own
// tests use it to stand up a fake server), but also there for the same
// "self-hosted mirror" reasoning as
// musicbrainz.NewClientWithBaseURL.
func NewClientWithBaseURL(cacheDir, userAgent, baseURL string, audiodbClient *audiodb.Client) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		userAgent:  userAgent,
		cacheDir:   cacheDir,
		baseURL:    baseURL,
		audiodb:    audiodbClient,
	}
}

// GetFrontCover returns the local filesystem path and content type of
// releaseMBID's front cover, preferring TheAudioDB's own cover art for
// releaseGroupMBID (the release group as a whole — TheAudioDB doesn't
// distinguish specific editions the way Cover Art Archive does) and
// falling back to Cover Art Archive, by the specific releaseMBID, when
// TheAudioDB doesn't have one. Cached to disk (under releaseMBID's
// identity, whichever source it actually came from) on first request.
// Returns ErrNoCoverArt if neither source has it. releaseGroupMBID may be
// empty to skip straight to Cover Art Archive (e.g. a caller that only has
// the release MBID in hand).
//
// The two sources are negative-cached separately (noAudioDBSentinelExt,
// keyed by releaseGroupMBID; noCoverSentinelExt, keyed by releaseMBID) and
// TheAudioDB is always tried first, ahead of Cover Art Archive's own
// sentinel check — found live: with one shared sentinel gating both
// sources, a release Cover Art Archive had already 404'd (cached before
// TheAudioDB support existed, or before TheAudioDB itself gained this
// release) permanently skipped ever trying TheAudioDB too, even on a
// build that could have found it, for up to noCoverRecheckAfter. Querying
// TheAudioDB first every time a cover is genuinely missing would reopen
// exactly the "hammer the network for a release that's never getting
// art" cost that sentinel exists to avoid — so TheAudioDB gets its own
// independent miss-cache instead of sharing Cover Art Archive's.
func (c *Client) GetFrontCover(ctx context.Context, releaseGroupMBID, releaseMBID string) (path string, contentType string, err error) {
	if releaseMBID == "" {
		return "", "", fmt.Errorf("coverart: releaseMBID must not be empty")
	}

	if cached, ct, ok := c.checkCache(releaseMBID); ok {
		return cached, ct, nil
	}

	if c.audiodb != nil && releaseGroupMBID != "" && !c.hasNoAudioDBSentinel(releaseGroupMBID) {
		meta, err := c.audiodb.LookupAlbumByReleaseGroupMBID(ctx, releaseGroupMBID)
		switch {
		case err != nil:
			// A TheAudioDB failure (network, rate limit, etc.) degrades to
			// the Cover Art Archive fallback below rather than failing the
			// whole request, and isn't cached as a miss — the same "best-
			// effort, never worse than before this existed" spirit as every
			// other TheAudioDB call in this codebase. A genuine "no thumb"
			// answer (the other case below) is a real result worth caching;
			// a transient failure to even ask isn't.
		case meta == nil || meta.ThumbURL == "":
			c.writeNoAudioDBSentinel(releaseGroupMBID)
		default:
			if path, ct, err := c.downloadAndCache(ctx, meta.ThumbURL, releaseMBID); err == nil {
				return path, ct, nil
			}
		}
	}

	if c.hasNoCoverSentinel(releaseMBID) {
		return "", "", ErrNoCoverArt
	}
	return c.fetchFromCoverArtArchive(ctx, releaseMBID)
}

// Refetch forces a fresh, live check of both sources for releaseMBID's
// front cover right now, ignoring whatever's currently cached — a cached
// image, either sentinel, or both. GetFrontCover's own dual-sentinel
// design (see its doc comment) already self-heals a stale miss on its
// own, but only after noCoverRecheckAfter (30 days) — this is the album
// page's own "retry cover art" action, for a user who doesn't want to
// wait that long on the chance either provider's catalog has since grown
// to include this release. Returns ErrNoCoverArt the same as GetFrontCover
// when a fresh check of both sources still turns up nothing.
func (c *Client) Refetch(ctx context.Context, releaseGroupMBID, releaseMBID string) (path, contentType string, err error) {
	if err := c.DeleteCached(releaseMBID); err != nil {
		return "", "", err
	}
	// DeleteCached deliberately leaves noAudioDBSentinelExt alone (see its
	// own doc comment — it outliving one release's removal is harmless) —
	// but a genuine retry needs both sentinels gone, or a still-fresh
	// noAudioDBSentinelExt would silently skip re-checking TheAudioDB here.
	if releaseGroupMBID != "" {
		if err := os.Remove(filepath.Join(c.cacheDir, releaseGroupMBID+noAudioDBSentinelExt)); err != nil && !os.IsNotExist(err) {
			return "", "", fmt.Errorf("delete no-audiodb sentinel: %w", err)
		}
	}
	return c.GetFrontCover(ctx, releaseGroupMBID, releaseMBID)
}

// DeleteCached removes releaseMBID's cached front cover (any of the known
// extensions) and its no-cover sentinel, if either exists — used when an
// artist is removed so its albums' cover art doesn't outlive the artist
// (see internal/api's handleRemoveMusicArtist). Not an error if nothing
// was cached for this release to begin with. Deliberately doesn't also
// remove a noAudioDBSentinelExt file for the release's group: that
// sentinel carries no identity of its own beyond a MusicBrainz release
// group MBID (the same content-addressed answer holds regardless of which
// artist row happens to reference it), so a stray one outliving the
// artist that first triggered it is harmless — a saved lookup if the same
// release group is ever added again, not stale data.
func (c *Client) DeleteCached(releaseMBID string) error {
	if releaseMBID == "" {
		return nil
	}
	for ext := range extToContentType {
		p := filepath.Join(c.cacheDir, releaseMBID+ext)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete cached cover art %s: %w", p, err)
		}
	}
	sentinel := filepath.Join(c.cacheDir, releaseMBID+noCoverSentinelExt)
	if err := os.Remove(sentinel); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete no-cover sentinel %s: %w", sentinel, err)
	}
	return nil
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

// hasNoCoverSentinel reports whether releaseMBID has a still-fresh "no
// cover art" sentinel — false for a missing one (obviously) but also for
// one older than noCoverRecheckAfter, so GetFrontCover gives it a real
// live re-check instead of trusting a stale miss forever. A sentinel that
// gets rewritten (fetchFromCoverArtArchive overwrites it on every fresh
// 404) naturally resets its own clock for another noCoverRecheckAfter.
func (c *Client) hasNoCoverSentinel(releaseMBID string) bool {
	info, err := os.Stat(filepath.Join(c.cacheDir, releaseMBID+noCoverSentinelExt))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < noCoverRecheckAfter
}

// hasNoAudioDBSentinel is hasNoCoverSentinel for TheAudioDB's own miss
// cache — same staleness rule (noCoverRecheckAfter), keyed by
// releaseGroupMBID instead of releaseMBID (see noAudioDBSentinelExt).
func (c *Client) hasNoAudioDBSentinel(releaseGroupMBID string) bool {
	info, err := os.Stat(filepath.Join(c.cacheDir, releaseGroupMBID+noAudioDBSentinelExt))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < noCoverRecheckAfter
}

// writeNoAudioDBSentinel records that TheAudioDB has confirmed no album
// thumb for releaseGroupMBID — best-effort: a write failure here just
// means the next call pays for another live lookup, not a correctness
// problem worth surfacing as an error GetFrontCover would otherwise have
// to decide how to handle.
func (c *Client) writeNoAudioDBSentinel(releaseGroupMBID string) {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.cacheDir, releaseGroupMBID+noAudioDBSentinelExt), nil, 0o644)
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

// fetchFromCoverArtArchive is the original, sole source of cover art
// before TheAudioDB support existed — Cover Art Archive's own release-
// keyed endpoint, where a 404 is a definitive "no cover art" answer worth
// caching as a sentinel (unlike a generic download failure, see
// downloadAndCache).
func (c *Client) fetchFromCoverArtArchive(ctx context.Context, releaseMBID string) (path, contentType string, err error) {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cover art cache dir: %w", err)
	}

	// The "-250" thumbnail, not the full-size image — CantiNode only ever
	// displays this at library-grid thumbnail size, and a release's
	// original scan can be tens of megabytes.
	url := fmt.Sprintf("%s/release/%s/front-250", c.baseURL, releaseMBID)
	resp, err := c.fetch(ctx, url)
	if err != nil {
		return "", "", err
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
	return c.writeToCache(resp, releaseMBID)
}

// downloadAndCache fetches an arbitrary already-known image URL (e.g.
// TheAudioDB's own album thumb, which unlike Cover Art Archive doesn't
// have a predictable per-release URL scheme to build) and caches it under
// cacheKey. A non-200 here is just a generic failure — not cached as a
// "no cover art" sentinel the way Cover Art Archive's 404 is, since it
// doesn't carry the same definitive meaning; the caller falls back to
// fetchFromCoverArtArchive instead.
func (c *Client) downloadAndCache(ctx context.Context, url, cacheKey string) (path, contentType string, err error) {
	if err := os.MkdirAll(c.cacheDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cover art cache dir: %w", err)
	}
	resp, err := c.fetch(ctx, url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("coverart: unexpected status %d for %s", resp.StatusCode, url)
	}
	return c.writeToCache(resp, cacheKey)
}

func (c *Client) fetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch cover art: %w", err)
	}
	return resp, nil
}

// writeToCache streams resp's body to cacheDir under cacheKey, the
// extension chosen from its Content-Type (defaulting to .jpg for an
// unrecognized one — both Cover Art Archive and TheAudioDB serve jpeg in
// the overwhelming majority of cases). resp.Body is the caller's to close.
func (c *Client) writeToCache(resp *http.Response, cacheKey string) (path, contentType string, err error) {
	ct := resp.Header.Get("Content-Type")
	ext, ok := contentTypeToExt[ct]
	if !ok {
		ext = ".jpg"
		ct = "image/jpeg"
	}

	dest := filepath.Join(c.cacheDir, cacheKey+ext)
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
