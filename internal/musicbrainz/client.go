// Package musicbrainz is a thin client for the MusicBrainz web service
// (https://musicbrainz.org/doc/MusicBrainz_API), used to match a scanned
// audio file to a canonical artist/release/recording and to fetch the
// metadata CantiNode's library is built from.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://musicbrainz.org/ws/2"

// VariousArtistsMBID is MusicBrainz's own special-purpose "Various Artists"
// artist — the credited artist on any release whose actual performers vary
// by track (most compilations), the same universal ID on every MusicBrainz
// server (https://musicbrainz.org/artist/89ad4ac3-39f7-470e-963a-56509c546377),
// not something CantiNode assigns itself. Not a real artist with a
// discography of its own worth tracking: its "releases" are every
// multi-performer compilation MusicBrainz has ever cataloged (tens of
// thousands), so treating it like an ordinary artist for Missing-list
// purposes would flood that artist's page with virtually every compilation
// that exists rather than anything the user could plausibly want. See
// internal/discography.Service.RefreshArtist's own use of this constant to
// skip discography caching for it specifically.
const VariousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"

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

// batchLookupChunkSize bounds how many recording MBIDs go into one
// rid:(... OR ...) search query — comfortably under any practical URL
// length limit for a Lucene OR-clause of UUIDs.
const batchLookupChunkSize = 50

// BatchLookupRecordings resolves many recording MBIDs in as few requests as
// possible, via MusicBrainz's rid:(id1 OR id2 OR ...) search syntax
// (verified live against the real API to return full inc=artist-credits+
// releases+release-groups data, same as LookupRecording) — a folder of N
// tagged files can resolve their embedded recording IDs in one request
// instead of N, each otherwise paying MusicBrainz's own ~1 req/sec throttle.
// ids is chunked at batchLookupChunkSize per request. An id with no match
// (deleted/merged MBID) is simply absent from the returned map, not an
// error. A chunk request failure returns immediately — no partial results
// are returned on error, keeping the failure mode simple for callers to
// reason about (see internal/musicscanner's own fallback-to-per-file
// behavior on error).
func (c *Client) BatchLookupRecordings(ctx context.Context, ids []string) (map[string]Recording, error) {
	out := make(map[string]Recording, len(ids))
	for start := 0; start < len(ids); start += batchLookupChunkSize {
		end := start + batchLookupChunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]

		terms := make([]string, len(chunk))
		for i, id := range chunk {
			terms[i] = id
		}
		query := "rid:(" + strings.Join(terms, " OR ") + ")"

		body, err := c.get(ctx, "/recording/", url.Values{
			"query": {query},
			"inc":   {"artist-credits+releases+release-groups"},
			"fmt":   {"json"},
			"limit": {fmt.Sprintf("%d", batchLookupChunkSize)},
		})
		if err != nil {
			return nil, fmt.Errorf("batch lookup recordings: %w", err)
		}
		var resp recordingSearchResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode batch recording lookup: %w", err)
		}
		for _, rec := range resp.Recordings {
			out[rec.ID] = rec
		}
	}
	return out, nil
}

// LookupArtist fetches a single artist by MBID — identity plus genres/
// tags/rating — used by internal/acquisition to seed a newly monitored
// artist, and by internal/api.refreshMusicArtistMetadata to cache
// everything about the artist worth keeping, even fields nothing displays
// yet. Does NOT include release groups — see BrowseArtistReleaseGroups.
func (c *Client) LookupArtist(ctx context.Context, mbid string) (*Artist, error) {
	body, err := c.get(ctx, "/artist/"+url.PathEscape(mbid), url.Values{
		"inc": {"genres+tags+ratings"},
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

// ErrSeriesHasNoReleaseGroups means mbid resolved to a real MusicBrainz
// Series, but not one CantiNode can track as a discography source — either
// it links nothing but entity types that don't resolve to an album at all
// (a Series can just as well link Recordings, Works, or Events instead of
// Release Groups/Releases), every one of its Release entries failed the
// extra release-group resolution lookup, or it links nothing at all. A
// series MBID that doesn't exist, or an MBID for a different entity type
// entirely (an artist, a release, ...), fails LookupSeries with an
// ordinary MusicBrainz 404 instead — series/artist/release MBIDs live in
// disjoint UUID space, so MusicBrainz itself already tells those apart.
var ErrSeriesHasNoReleaseGroups = errors.New("musicbrainz: series has no release-group entries")

// LookupSeries fetches mbid's full release-group membership in one
// unpaginated call (verified live against a real 87-entry series — a
// Series lookup's relations aren't paged the way a Browse response is).
// See musiclibrary.Artist.Kind's own doc comment for why CantiNode tracks
// a Series as a synthetic library "artist." Understands both series kinds
// that link release groups (see Series.Type's own doc comment): a
// "release_group" relation is used as-is; a "release" relation (a
// "Release series") needs one further lookup per entry to resolve its own
// release group, since MusicBrainz doesn't nest that inside a series
// relation at all — a real, sequential, rate-limited cost this incurs only
// for that series kind, best-effort (an entry whose extra lookup fails is
// skipped rather than failing the whole series). Both kinds' relations are
// merged, deduplicated by release group (lowest ordering-key wins), and
// sorted by ordering-key ascending.
func (c *Client) LookupSeries(ctx context.Context, mbid string) (*Series, error) {
	body, err := c.get(ctx, "/series/"+url.PathEscape(mbid), url.Values{
		"inc": {"release-group-rels+release-rels+artist-credits"},
		"fmt": {"json"},
	})
	if err != nil {
		return nil, fmt.Errorf("lookup series %s: %w", mbid, err)
	}
	var resp seriesLookupResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode series %s: %w", mbid, err)
	}

	byReleaseGroup := make(map[string]SeriesReleaseGroupRelation, len(resp.Relations))
	for _, rel := range resp.Relations {
		var entry SeriesReleaseGroupRelation
		switch rel.TargetType {
		case "release_group":
			entry = SeriesReleaseGroupRelation{
				OrderingKey:      rel.OrderingKey,
				ReleaseGroupMBID: rel.ReleaseGroup.ID,
				Title:            rel.ReleaseGroup.Title,
				PrimaryType:      rel.ReleaseGroup.PrimaryType,
				SecondaryTypes:   rel.ReleaseGroup.SecondaryTypes,
				FirstReleaseDate: rel.ReleaseGroup.FirstReleaseDate,
				ArtistCredit:     rel.ReleaseGroup.ArtistCredit,
			}
		case "release":
			rg, err := c.lookupReleaseGroupForRelease(ctx, rel.Release.ID)
			if err != nil {
				continue // best-effort: one bad/deleted release entry doesn't sink the whole series
			}
			entry = SeriesReleaseGroupRelation{
				OrderingKey:      rel.OrderingKey,
				ReleaseGroupMBID: rg.ID,
				Title:            rg.Title,
				PrimaryType:      rg.PrimaryType,
				SecondaryTypes:   rg.SecondaryTypes,
				FirstReleaseDate: rg.FirstReleaseDate,
				ArtistCredit:     rel.Release.ArtistCredit,
			}
		default:
			continue
		}
		if existing, ok := byReleaseGroup[entry.ReleaseGroupMBID]; !ok || entry.OrderingKey < existing.OrderingKey {
			byReleaseGroup[entry.ReleaseGroupMBID] = entry
		}
	}
	if len(byReleaseGroup) == 0 {
		return nil, ErrSeriesHasNoReleaseGroups
	}
	relations := make([]SeriesReleaseGroupRelation, 0, len(byReleaseGroup))
	for _, entry := range byReleaseGroup {
		relations = append(relations, entry)
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].OrderingKey < relations[j].OrderingKey })

	return &Series{
		ID:             resp.ID,
		Type:           resp.Type,
		Disambiguation: resp.Disambiguation,
		Name:           resp.Name,
		Relations:      relations,
	}, nil
}

// lookupReleaseGroupForRelease resolves releaseMBID's own release group —
// deliberately lighter than LookupReleaseWithTracklist (no media/
// tracklist), used only by LookupSeries's "Release series" handling.
func (c *Client) lookupReleaseGroupForRelease(ctx context.Context, releaseMBID string) (ReleaseGroupSummary, error) {
	body, err := c.get(ctx, "/release/"+url.PathEscape(releaseMBID), url.Values{
		"inc": {"release-groups"},
		"fmt": {"json"},
	})
	if err != nil {
		return ReleaseGroupSummary{}, fmt.Errorf("lookup release group for release %s: %w", releaseMBID, err)
	}
	var resp releaseWithReleaseGroupResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return ReleaseGroupSummary{}, fmt.Errorf("decode release %s: %w", releaseMBID, err)
	}
	if resp.ReleaseGroup.ID == "" {
		return ReleaseGroupSummary{}, fmt.Errorf("release %s has no release group", releaseMBID)
	}
	return resp.ReleaseGroup, nil
}

// browseLimit is MusicBrainz's own maximum page size for a browse
// request — shared by BrowseArtistReleaseGroups and
// BrowseReleaseGroupReleases, both of which need every page of a
// potentially large result set: the highest single-page cost minimizes
// the number of pages (and so the number of rate-limited round trips)
// either one needs.
const browseLimit = 100

// browseMaxPages bounds how many pages either browse loop will ever fetch
// (100 × 100 = 10,000 rows) — a sanity ceiling against a malformed
// response looping forever, not a limit any real artist's discography or
// release group's edition count should ever approach.
const browseMaxPages = 100

// BrowseArtistReleaseGroups returns mbid's ENTIRE release-group list,
// fully paginated — MusicBrainz's browse response reports the artist's
// true total count (release-group-count) separately from how many a single
// page returns, so this keeps fetching successive pages (each its own
// rate-limited request) until every one has been collected. This is the
// fix for a real bug: LookupArtist's inc=release-groups sub-resource is
// silently capped at MusicBrainz's default page size (25) with no way to
// ask for more from that endpoint — every artist's cached discography was
// truncated to (at most) its first 25 release groups, in whatever order
// MusicBrainz happened to return them, regardless of the real total. A
// prolific artist can have hundreds; this fetches all of them.
func (c *Client) BrowseArtistReleaseGroups(ctx context.Context, mbid string) ([]ReleaseGroupSummary, error) {
	var out []ReleaseGroupSummary
	for page := 0; page < browseMaxPages; page++ {
		offset := page * browseLimit
		body, err := c.get(ctx, "/release-group/", url.Values{
			"artist": {mbid},
			"limit":  {fmt.Sprintf("%d", browseLimit)},
			"offset": {fmt.Sprintf("%d", offset)},
			"fmt":    {"json"},
		})
		if err != nil {
			return nil, fmt.Errorf("browse artist %s release groups (offset %d): %w", mbid, offset, err)
		}
		var resp releaseGroupBrowseResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode artist %s release groups (offset %d): %w", mbid, offset, err)
		}
		out = append(out, resp.ReleaseGroups...)
		if len(resp.ReleaseGroups) == 0 || len(out) >= resp.Count {
			break
		}
	}
	return out, nil
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
// BrowseReleaseGroupReleases returns releaseGroupMBID's ENTIRE release
// list, fully paginated — mirrors BrowseArtistReleaseGroups' own fix for
// the identical bug: a browse response reports the release group's true
// total (release-count) separately from how many one page returns, so a
// single request capped at even MusicBrainz's own maximum page size (100)
// still silently truncates a heavily-reissued release group (100+
// pressings/editions isn't rare for a classic album). Every page is its
// own rate-limited request.
func (c *Client) BrowseReleaseGroupReleases(ctx context.Context, releaseGroupMBID string) ([]ReleaseSearchResult, error) {
	var out []ReleaseSearchResult
	for page := 0; page < browseMaxPages; page++ {
		offset := page * browseLimit
		body, err := c.get(ctx, "/release/", url.Values{
			"release-group": {releaseGroupMBID},
			"inc":           {"media"},
			"fmt":           {"json"},
			"limit":         {fmt.Sprintf("%d", browseLimit)},
			"offset":        {fmt.Sprintf("%d", offset)},
		})
		if err != nil {
			return nil, fmt.Errorf("browse release-group %s releases (offset %d): %w", releaseGroupMBID, offset, err)
		}
		var resp releaseBrowseResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode release-group %s releases (offset %d): %w", releaseGroupMBID, offset, err)
		}
		out = append(out, resp.Releases...)
		if len(resp.Releases) == 0 || len(out) >= resp.Count {
			break
		}
	}
	return out, nil
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
