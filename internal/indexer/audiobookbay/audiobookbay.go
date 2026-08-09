// Package audiobookbay is a native indexer for AudioBook Bay (ABB): an
// audiobook torrent site with no Newznab/Torznab API, so Prowlarr can't reach
// it. ABB never publishes a .torrent or a ready magnet — a release page carries
// the info hash and a tracker list, and the magnet is assembled from them here
// (the exact step that breaks a Prowlarr→Jackett definition). The result is an
// ordinary torrent Release that rides LibriNode's existing qBittorrent path.
//
// Ban avoidance is the whole game with ABB, and mirrors how a browser behaves:
//   - Search hits the site ONCE (the results listing). It does NOT crawl every
//     result's detail page — that per-search fan-out is what earns an IP ban.
//     Each release's magnet is assembled lazily at grab time (Resolve), for the
//     one release actually grabbed.
//   - Every request rides a warmed-up session (a PHPSESSID cookie fetched from
//     the homepage first) with full browser headers; ABB serves reliable
//     search pages only to an initialized, browser-like session.
//   - The query is lowercased before it hits the URL: ABB's edge redirects
//     any ?s= value starting with an uppercase letter straight to the
//     homepage (confirmed live), and book titles are naturally Title Case.
//     Unlowercased, this bounces nearly every real search — the redirect
//     looks identical to rate-limiting but isn't; it's a fixed property of
//     the query, not a transient IP state, so no amount of retrying helps it.
//   - A search redirected to the homepage, or one that comes back with an
//     empty body, means ABB is rate-limiting/blocking (common on a shared VPN
//     exit IP) — this is the transient case, once the query-case issue above
//     is ruled out. Both signals get one gentle retry with backoff — a
//     browser-like "try again" — and only then surface a clear error, so a
//     transient bounce doesn't fail an otherwise-working source. The
//     grab-time detail fetch (Resolve) gets the same one-retry treatment for
//     the same reason.
//
// This is a dual-use shadow-library source: it is never bundled or enabled by
// default; a user adds it deliberately and is responsible for its use. HTML
// selectors target ABB's known layout and may need updating if the site
// changes — the inherent fragility of a scraped source.
package audiobookbay

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/indexer"
)

const (
	// Name is both the registry key and the stored indexer type.
	Name = "audiobookbay"
	// DefaultBaseURL is ABB's domain at time of writing; it rotates, so the
	// user can override it on the indexer (Settings → Indexers → Site URL).
	DefaultBaseURL = "https://audiobookbay.lu"

	// A current desktop-Chrome UA — ABB filters obvious scrapers.
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	// legacyCategory is the category query ABB's search expects (already
	// percent-encoded); shelfmark found it necessary for reliable results.
	legacyCategory = "undefined%2Cundefined"

	// searchAttempts is how many times a homepage-bounced or empty search is
	// retried before surfacing the rate-limit error. ABB rate-limits per IP
	// once it sees a burst, so retrying hard is counterproductive — each
	// attempt is another warm-up + search, and a storm of them keeps the
	// throttle alive. One retry, well spaced, catches a genuinely transient
	// blip without feeding the limiter.
	searchAttempts = 2
	// searchBackoff scales the pause between retries: attempt N waits N× this.
	// Long enough to let a short throttle window clear before the single retry.
	searchBackoff = 2 * time.Second
	// detailAttempts mirrors searchAttempts for Resolve's grab-time detail
	// fetch: one gentle retry (same searchBackoff pacing) on a transient
	// empty page or a missing hash, without reintroducing the retry-burst
	// that searchAttempts was deliberately tuned down to avoid.
	detailAttempts = 2
)

// defaultTrackers back-fill a magnet when a release page lists none, so the
// torrent can still find peers.
var defaultTrackers = []string{
	"udp://tracker.openbittorrent.com:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.coppersurfer.tk:6969/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.internetwarriors.net:1337/announce",
}

// Def is the native-indexer definition; register it with indexer.RegisterNative.
func Def() indexer.NativeDef {
	return indexer.NativeDef{
		Name:           Name,
		DisplayName:    "AudioBook Bay",
		Protocol:       indexer.ProtocolTorrent,
		MediaTypes:     []string{"audiobook"},
		DefaultBaseURL: DefaultBaseURL,
		WIP:            true,
		New: func(ind *indexer.Indexer, httpc *http.Client) indexer.Searcher {
			return &searcher{ind: ind, bases: ParseBases(ind.BaseURL), httpc: httpc}
		},
	}
}

// ParseBases splits the indexer's base-URL field — a primary site URL plus
// optional comma-separated fallbacks (ABB runs several mirror domains) — into
// a cleaned, ordered list. Empty input yields the default site.
func ParseBases(raw string) []string {
	bases := []string{}
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimRight(strings.TrimSpace(part), "/"); p != "" {
			bases = append(bases, p)
		}
	}
	if len(bases) == 0 {
		bases = []string{DefaultBaseURL}
	}
	return bases
}

type searcher struct {
	ind   *indexer.Indexer
	bases []string // site URLs, tried in order (primary first, then fallbacks)
	httpc *http.Client
}

// session returns a client with its own fresh cookie jar (so a search's homepage
// warm-up + listing, and a grab's warm-up + detail, share one PHPSESSID like a
// browser tab) AND its own connection — keep-alives are disabled.
//
// The app-wide indexer client pools keep-alive connections for its whole
// lifetime. AudioBook Bay (Cloudflare-fronted) throttles a connection once it
// has served enough requests: the reused connection then quietly returns empty
// result pages or bounces the search to the homepage, while a *fresh* connection
// keeps working — which is why a browser, curl, and a just-started process all
// succeed against the exact same site and IP where a long-running server fails.
// Opening a fresh connection per request sidesteps that entirely; a scraped
// source is low-volume, so the extra handshakes cost nothing that matters.
func (s *searcher) session() *http.Client {
	c := *s.httpc
	c.Transport = &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
	}
	if jar, err := cookiejar.New(nil); err == nil {
		c.Jar = jar
	}
	return &c
}

// Test confirms the site is reachable on at least one configured URL.
func (s *searcher) Test(ctx context.Context) error {
	var err error
	for _, base := range s.bases {
		if _, _, err = fetch(ctx, s.session(), base+"/"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no configured site URL answered (tried %d): %w", len(s.bases), err)
}

// Search finds audiobook torrents from ONE listing request per site (after a
// session warm-up). Each result's magnet is deferred to Resolve (grab time),
// so a search never crawls detail pages — the behaviour that gets ABB to ban an
// IP. The first configured site that answers serves the search; a homepage
// redirect means the search was blocked/rate-limited.
func (s *searcher) Search(ctx context.Context, query, mediaType string) ([]indexer.Release, error) {
	if mediaType != "audiobook" {
		return nil, nil
	}
	var lastErr error
	for _, base := range s.bases {
		posts, err := s.searchBase(ctx, base, query)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		releases := make([]indexer.Release, 0, len(posts))
		for _, p := range posts {
			rel := indexer.Release{
				IndexerID: s.ind.ID,
				Indexer:   s.ind.Name,
				Protocol:  indexer.ProtocolTorrent,
				Title:     p.Title,
				Keywords:  p.Keywords,
				GUID:      p.URL,
				InfoURL:   p.URL,
				// The release page itself — Resolve turns it into a magnet at
				// grab time (one request, only when grabbed).
				DownloadURL: p.URL,
				Size:        p.Size,
				Seeders:     -1, // ABB doesn't report swarm health; unknown, not dead
				Peers:       -1,
			}
			if !p.PostedDate.IsZero() {
				rel.PublishDate = p.PostedDate.Format(time.RFC3339)
			}
			releases = append(releases, rel)
		}
		return releases, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no configured site URL answered")
	}
	return nil, lastErr
}

// searchBase warms a fresh session and runs ONE listing request against a site.
// A homepage bounce (ABB rate-limiting/blocking, common on a shared VPN IP) is
// retried a few times with backoff — re-warming a fresh session each time, the
// way hitting "search" again in a browser would — before giving up. A warm-up or
// listing transport error isn't retried here; the caller falls to the next site.
func (s *searcher) searchBase(ctx context.Context, base, query string) ([]post, error) {
	var lastErr error
	for attempt := 0; attempt < searchAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * searchBackoff):
			}
		}
		client := s.session()
		// Warm up the session from the homepage first, then search on it.
		if _, _, err := fetch(ctx, client, base+"/"); err != nil {
			slog.Warn("abb warmup failed", "base", base, "attempt", attempt+1, "err", err)
			return nil, err
		}
		// ABB's edge redirects any ?s= value starting with an uppercase ASCII
		// letter straight to the homepage — confirmed live: "dune", "Dune",
		// "dune+MESSIAH" and "1dune" all search fine, but "Dune", "DUNE", and
		// "Abcdef" all bounce. Book titles are naturally Title Case, so an
		// unlowercased query bounces essentially every real search. Lowercase
		// the whole query (matching the shelfmark reference client) rather
		// than just the first rune, to not depend on this edge case being
		// exactly that narrow forever.
		searchURL := base + "/?s=" + url.QueryEscape(strings.ToLower(query)) + "&cat=" + legacyCategory
		listing, finalURL, err := fetch(ctx, client, searchURL)
		if err != nil {
			slog.Warn("abb search fetch failed", "base", base, "attempt", attempt+1, "url", searchURL, "err", err)
			return nil, err
		}
		redirected := isHomepageRedirect(finalURL, base)
		slog.Info("abb search attempt", "base", base, "attempt", attempt+1, "url", searchURL,
			"finalURL", finalURL, "redirected", redirected, "bytes", len(listing))
		if redirected {
			lastErr = fmt.Errorf("AudioBook Bay redirected the search to its homepage — it is likely rate-limiting or temporarily blocking this IP; try again later")
			continue
		}
		if strings.TrimSpace(listing) == "" {
			lastErr = fmt.Errorf("AudioBook Bay returned an empty search page — it is likely rate-limiting or temporarily blocking this IP; try again later")
			continue
		}
		posts := parseListing(listing, base)
		// Soft-block guard (the shelfmark reference client's key trick): when ABB
		// throttles an IP it can serve its homepage "Latest" feed at the search
		// URL — HTTP 200, no redirect, a normal-looking listing — instead of real
		// results. isHomepageRedirect can't catch that (the URL never changes),
		// so without this check those unrelated posts leak through as matches and
		// the grab downstream rejects them: the search "works" but the user sees
		// nothing relevant. If not one parsed post shares a word with the query,
		// treat it as a throttle and retry on a fresh session, same as an empty
		// page or a redirect.
		if len(posts) > 0 && !anyRelevant(posts, query) {
			slog.Info("abb search only unrelated results (likely homepage feed on a throttled IP)",
				"base", base, "attempt", attempt+1, "posts", len(posts))
			lastErr = fmt.Errorf("AudioBook Bay returned only results unrelated to the query — it is likely rate-limiting or temporarily blocking this IP; try again later")
			continue
		}
		slog.Info("abb search parsed", "base", base, "attempt", attempt+1, "posts", len(posts))
		return posts, nil
	}
	return nil, lastErr
}

// Resolve turns a release-page URL into an assembled magnet — called at grab
// time for exactly the release the user grabbed. It warms a fresh session, then
// fetches that one page for its info hash + trackers. A blank page (the same
// throttle signature a search sees) or a page with no readable hash gets one
// gentle retry on a fresh session before surfacing the error, mirroring
// searchBase — this is a single grab-time request, not a per-search fan-out,
// so the extra attempt doesn't reintroduce the retry-burst problem.
func (s *searcher) Resolve(ctx context.Context, downloadURL string) (string, error) {
	if strings.HasPrefix(downloadURL, "magnet:") {
		return downloadURL, nil // already resolved
	}
	u, err := url.Parse(downloadURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid AudioBook Bay release URL")
	}
	home := u.Scheme + "://" + u.Host + "/"

	var lastErr error
	for attempt := 0; attempt < detailAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * searchBackoff):
			}
		}
		client := s.session()
		_, _, warmErr := fetch(ctx, client, home) // best-effort warm-up
		page, finalURL, err := fetch(ctx, client, downloadURL)
		if err != nil {
			slog.Warn("abb resolve fetch failed", "url", downloadURL, "attempt", attempt+1, "warmErr", warmErr, "err", err)
			lastErr = fmt.Errorf("fetching AudioBook Bay release page: %w", err)
			continue
		}
		slog.Info("abb resolve attempt", "url", downloadURL, "attempt", attempt+1,
			"warmErr", warmErr, "finalURL", finalURL, "bytes", len(page))
		if strings.TrimSpace(page) == "" {
			lastErr = fmt.Errorf("AudioBook Bay returned an empty release page — it is likely rate-limiting or temporarily blocking this IP; try again later")
			continue
		}
		hash, trackers, _, ok := parseDetail(page)
		if !ok {
			slog.Warn("abb resolve no hash", "url", downloadURL, "attempt", attempt+1, "bytes", len(page))
			lastErr = fmt.Errorf("no info hash on the AudioBook Bay release page (its layout may have changed)")
			continue
		}
		if len(trackers) == 0 {
			trackers = defaultTrackers
		}
		return buildMagnet(hash, titleFromURL(u), trackers), nil
	}
	return "", lastErr
}

// fetch GETs a URL on the given session client and returns the body plus the
// final URL (after redirects), for homepage-redirect detection. Headers are kept
// deliberately minimal — a browser User-Agent and Accept, nothing more. The
// shelfmark reference client (and a plain curl) get search results this way,
// whereas the fuller "browser navigation" header set (Sec-Fetch-*, Referer,
// Upgrade-Insecure-Requests) paired with a Go client's TLS/HTTP fingerprint
// reads as a spoofed browser and gets the search bounced to the homepage.
func fetch(ctx context.Context, client *http.Client, rawURL string) (body, finalURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", "", fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return string(b), final, nil
}

// queryWords splits a query into lowercased words long enough to discriminate a
// relevant result — 1–2 char words ("a", "of", "to") match almost any title and
// so are dropped from the relevance check.
func queryWords(query string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len([]rune(w)) > 2 {
			out = append(out, w)
		}
	}
	return out
}

// anyRelevant reports whether at least one post's title shares a meaningful word
// with the query. It's the discriminator for the soft-block guard in searchBase:
// a real search returns posts whose titles echo the query, whereas ABB's
// homepage "Latest" feed (served on a throttled IP) is unrelated to it. When the
// query has no usable words to match on, relevance can't be judged and the
// posts are accepted as-is.
func anyRelevant(posts []post, query string) bool {
	words := queryWords(query)
	if len(words) == 0 {
		return true
	}
	for _, p := range posts {
		title := strings.ToLower(p.Title)
		for _, w := range words {
			if strings.Contains(title, w) {
				return true
			}
		}
	}
	return false
}

// isHomepageRedirect reports whether a search landed back on the site's
// homepage — ABB's tell for a blocked or rate-limited search.
func isHomepageRedirect(finalURL, base string) bool {
	fu, err := url.Parse(finalURL)
	if err != nil {
		return false
	}
	bu, err := url.Parse(base)
	if err != nil {
		return false
	}
	return strings.EqualFold(fu.Host, bu.Host) && (fu.Path == "" || fu.Path == "/") && fu.RawQuery == ""
}

// titleFromURL derives a magnet display name from a release page's slug.
func titleFromURL(u *url.URL) string {
	seg := strings.Trim(u.Path, "/")
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ReplaceAll(seg, "-", " ")
	if seg == "" {
		return "audiobook"
	}
	return seg
}

// --- Parsing (pure functions; fixture-tested) ---

type post struct {
	URL        string
	Title      string
	Keywords   string
	Size       int64
	PostedDate time.Time // zero if unparsed
}

var (
	// A listing's release links: ABB wraps each post title in
	// <div class="postTitle">…<a href="URL">Title</a>. Fallback below covers
	// bare /audio-books/ permalinks if the wrapper markup shifts.
	postTitleRe = regexp.MustCompile(`(?is)<div class="postTitle">.*?<a\s+href="([^"]+)"[^>]*>(.*?)</a>`)
	audioLinkRe = regexp.MustCompile(`(?i)<a\s+href="([^"]*/audio-?books?/[^"]+)"[^>]*>([^<]+)</a>`)
	tagRe       = regexp.MustCompile(`(?s)<[^>]+>`)

	// A detail page's 40- or 64-hex info hash (SHA1 or the newer v2/hybrid
	// SHA256): the "Info Hash:" label, then the hash within a short window
	// (lazily skipping any punctuation/markup between). Whitespace is allowed
	// between hex digits — ABB sometimes wraps the hash across markup — and
	// stripped before validation in parseDetail.
	infoHashRe = regexp.MustCompile(`(?is)info\s*hash.{0,80}?((?:[0-9a-f]\s*){40,64})`)
	// Fallback when the Info Hash table cell is missing or unparsable: some
	// release pages embed a complete magnet link instead. Matched on the raw
	// HTML since the link may live inside an href attribute.
	magnetHashRe = regexp.MustCompile(`(?i)magnet:\?[^"'<>\s]*xt=urn:btih:([0-9a-f]{40,64})`)
	// Tracker announce URLs (matched on the raw HTML, so hrefs count too).
	trackerRe = regexp.MustCompile(`(?i)(udp://[^\s"'<>]+|https?://[^\s"'<>]*announce[^\s"'<>]*)`)
	// A listing post's Keywords tag list, in its .postInfo block right after
	// the title — e.g. "Keywords: Dune Frank Herbert" for a post titled just
	// "Dune Messiah". Often names the author when the title alone doesn't;
	// parseListing scopes this match to one post's own segment of the HTML.
	keywordsRe = regexp.MustCompile(`(?is)keywords:\s*([^<]*)`)
	// "File Size: 512.5 MB" / "Size: 1.2 GB" (matched on tag-stripped text).
	sizeRe = regexp.MustCompile(`(?i)(?:file\s*)?size:?\s*([0-9][0-9.,]*)\s*(kb|mb|gb|tb)`)
	// "Posted: 27 Mar 2008" — a listing post's upload date, alongside its
	// Format/Bitrate/File Size in the same .postContent block. ABB gives no
	// time of day, so this parses to midnight UTC on that date.
	postedRe = regexp.MustCompile(`(?i)posted:\s*(\d{1,2}\s+[A-Za-z]+\s+\d{4})`)
)

// navPath marks hrefs that are site navigation, not release pages — ABB's
// category/tag/pagination links live under these segments and would otherwise
// slip through the fallback link matcher as bogus "posts".
var navPath = regexp.MustCompile(`(?i)/(?:type|tag|cat|category|page|member|profile)/`)

// parseListing extracts release-page links (absolute URLs), titles, and each
// post's Keywords, file size, and posted date from a search results page.
// Duplicate URLs and navigation links are dropped. Seeders/peers are never
// part of this: ABB (a WordPress listing, not a tracker) doesn't report swarm
// health anywhere on the site, listing or detail page.
func parseListing(html, base string) []post {
	seen := map[string]bool{}
	out := []post{}
	add := func(p post) {
		p.URL = absURL(base, p.URL)
		p.Title = cleanText(p.Title)
		p.Keywords = cleanText(p.Keywords)
		if p.URL == "" || p.Title == "" || seen[p.URL] || navPath.MatchString(p.URL) {
			return
		}
		seen[p.URL] = true
		out = append(out, p)
	}
	// Matched by index (not FindAllStringSubmatch) so each post's own HTML
	// segment — from the end of its title match to the start of the next
	// post's (or end of document) — can be searched for that post's Keywords,
	// size, and posted date without depending on a fixed div-nesting shape.
	matches := postTitleRe.FindAllStringSubmatchIndex(html, -1)
	for i, idx := range matches {
		href, title := html[idx[2]:idx[3]], html[idx[4]:idx[5]]
		segEnd := len(html)
		if i+1 < len(matches) {
			segEnd = matches[i+1][0]
		}
		segment := html[idx[1]:segEnd]
		keywords := ""
		if km := keywordsRe.FindStringSubmatch(segment); km != nil {
			keywords = km[1]
		}
		segText := stripTags(segment)
		var postedDate time.Time
		if pm := postedRe.FindStringSubmatch(segText); pm != nil {
			postedDate = parsePostedDate(pm[1])
		}
		add(post{URL: href, Title: title, Keywords: keywords, Size: parseSize(segText), PostedDate: postedDate})
	}
	if len(out) == 0 { // markup changed — fall back to permalink shape
		for _, m := range audioLinkRe.FindAllStringSubmatch(html, -1) {
			add(post{URL: m[1], Title: m[2]})
		}
	}
	return out
}

// parseDetail extracts the info hash, tracker list, and size from a release
// page. ok is false when no hash can be found (nothing to grab). The label and
// hash often straddle table tags, so the primary hash read is from tag-stripped
// text; if that comes up empty, a raw-HTML magnet link is the fallback (a few
// releases carry one instead of a usable Info Hash cell). Trackers are read
// from the raw HTML so URLs inside attributes still count.
func parseDetail(pageHTML string) (hash string, trackers []string, size int64, ok bool) {
	text := stripTags(pageHTML)
	if m := infoHashRe.FindStringSubmatch(text); m != nil {
		candidate := strings.ToLower(strings.Join(strings.Fields(m[1]), ""))
		if len(candidate) == 40 || len(candidate) == 64 {
			hash = candidate
		}
	}
	if hash == "" {
		if m := magnetHashRe.FindStringSubmatch(pageHTML); m != nil {
			hash = strings.ToLower(m[1])
		}
	}
	if hash == "" {
		return "", nil, 0, false
	}

	seen := map[string]bool{}
	for _, t := range trackerRe.FindAllString(pageHTML, -1) {
		t = strings.TrimRight(t, "/")
		if !seen[t] {
			seen[t] = true
			trackers = append(trackers, t)
		}
	}
	size = parseSize(text)
	return hash, trackers, size, true
}

// stripTags removes HTML tags and decodes entities, yielding plain text.
func stripTags(s string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(s, " "))
}

// buildMagnet assembles a magnet URI from the info hash, a display name, and
// trackers.
func buildMagnet(hash, title string, trackers []string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(strings.ToLower(hash))
	b.WriteString("&dn=")
	b.WriteString(magnetEscape(title))
	for _, tr := range trackers {
		b.WriteString("&tr=")
		b.WriteString(magnetEscape(tr))
	}
	return b.String()
}

// magnetEscape percent-encodes a magnet parameter. QueryEscape's '+' for
// spaces is form-encoding, which not every torrent client decodes inside a
// magnet URI — %20 is the universally-parsed form (what the *arr stack emits).
func magnetEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func parseSize(html string) int64 {
	m := sizeRe.FindStringSubmatch(html)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "kb":
		n *= 1 << 10
	case "mb":
		n *= 1 << 20
	case "gb":
		n *= 1 << 30
	case "tb":
		n *= 1 << 40
	}
	return int64(n)
}

// parsePostedDate parses ABB's "27 Mar 2008" listing date. Zero value on
// failure — a post's date is a nice-to-have for sorting/display, never worth
// failing the whole search over.
func parsePostedDate(s string) time.Time {
	t, err := time.Parse("2 Jan 2006", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// absURL resolves a possibly-relative href against the site base. It preserves
// the href's trailing slash: ABB's canonical release permalinks end in "/", and
// requesting the slash-less form returns a 301 with NO Location header — an
// unfollowable dead-end redirect — so the grab-time Resolve fetch of that URL
// would fail with "HTTP 301" and no magnet would ever be assembled.
func absURL(base, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if !strings.HasPrefix(href, "/") {
		href = "/" + href
	}
	return strings.TrimRight(base, "/") + href
}

// cleanText strips tags, decodes HTML entities, and collapses whitespace from a
// title fragment.
func cleanText(s string) string {
	return strings.Join(strings.Fields(stripTags(s)), " ")
}
