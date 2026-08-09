package download

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/redact"
)

// qBittorrent Web API v2: cookie-session auth via /api/v2/auth/login, then
// form-encoded endpoints under /api/v2. Notable quirk: torrents/add doesn't
// echo back the hash, so it's looked up right after by title (findHash) to
// give each grab a real client item id.
type qbittorrent struct {
	cfg   *ClientConfig
	httpc *http.Client
}

func newQBittorrent(cfg *ClientConfig) *qbittorrent {
	jar, _ := cookiejar.New(nil)
	return &qbittorrent{
		cfg: cfg,
		// A debrid bridge (TorBox/Real-Debrid presenting a qBittorrent API)
		// adds a magnet synchronously — it waits on the debrid service to
		// accept it, which routinely takes longer than a plain qBittorrent's
		// instant add. A short timeout fires mid-add: the torrent still lands,
		// but the grab goes unrecorded. Give adds generous headroom; List
		// bounds its own context so a hung bridge can't stall the import loop.
		httpc: &http.Client{Timeout: 120 * time.Second, Jar: jar},
	}
}

func (q *qbittorrent) base() string {
	return strings.TrimRight(q.cfg.Host, "/")
}

func (q *qbittorrent) login(ctx context.Context) error {
	form := url.Values{"username": {q.cfg.Username}, "password": {q.cfg.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		q.base()+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := q.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("qbittorrent: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "Ok") {
		return fmt.Errorf("qbittorrent: login failed (%d): %.80s", resp.StatusCode, body)
	}
	return nil
}

// do posts a form (or GETs when form is nil), logging in on the first 403.
func (q *qbittorrent) do(ctx context.Context, path string, form url.Values) ([]byte, error) {
	attempt := func() (*http.Response, error) {
		var req *http.Request
		var err error
		if form == nil {
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, q.base()+path, nil)
		} else {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost,
				q.base()+path, strings.NewReader(form.Encode()))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
		if err != nil {
			return nil, err
		}
		return q.httpc.Do(req)
	}

	resp, err := attempt()
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := q.login(ctx); err != nil {
			return nil, err
		}
		if resp, err = attempt(); err != nil {
			return nil, fmt.Errorf("qbittorrent: %w", err)
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("qbittorrent: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qbittorrent: %s: HTTP %d: %.100s", path, resp.StatusCode, body)
	}
	return body, nil
}

func (q *qbittorrent) Test(ctx context.Context) error {
	if err := q.login(ctx); err != nil {
		return err
	}
	_, err := q.do(ctx, "/api/v2/app/version", nil)
	return err
}

// Add sends a release to qBittorrent. Like the SABnzbd client, it resolves the
// release on LibriNode's side first: the download client is often a NAT'd/cloud
// client (or a debrid bridge) that can't fetch our LAN indexer/Prowlarr URL, so
// handing it that URL fails ("hostname could not be parsed") or stalls.
// LibriNode can reach the indexer, so it follows the URL to the magnet, or
// downloads the .torrent and uploads its bytes — either way the client gets
// something self-contained. A magnet URL is passed straight through; if
// resolution fails, it falls back to handing the client the URL.
func (q *qbittorrent) Add(ctx context.Context, dlURL, title string) (string, error) {
	// Make sure our category exists; qBittorrent 409s when it already does.
	_, _ = q.do(ctx, "/api/v2/torrents/createCategory",
		url.Values{"category": {q.cfg.Category}, "savePath": {""}})

	if strings.HasPrefix(dlURL, "magnet:") {
		return q.addURLs(ctx, dlURL, title)
	}
	if magnet, torrent, err := q.resolve(ctx, dlURL); err == nil {
		if magnet != "" {
			return q.addURLs(ctx, magnet, title)
		}
		if len(torrent) > 0 {
			return q.addFile(ctx, torrent, title)
		}
	}
	return q.addURLs(ctx, dlURL, title)
}

// magnetHashRe extracts a magnet URI's v1 ("btih") or v2 ("btmh") info hash —
// the same hex form AudioBookBay's own indexer already builds magnets from
// and extracts them with (see audiobookbay.go's magnetHashRe).
var magnetHashRe = regexp.MustCompile(`(?i)xt=urn:bt[im]h:([0-9a-f]{40,64})`)

// magnetHash returns a magnet URI's info hash, lowercased to match how
// clients report it, or "" if urls isn't a magnet (or carries no hash).
func magnetHash(urls string) string {
	m := magnetHashRe.FindStringSubmatch(urls)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// addURLs hands qBittorrent a magnet (or a URL it can fetch itself) via the
// urls field. A magnet's own info hash is exact and independent of whatever
// name the client ends up reporting for the torrent — a debrid bridge
// (TorBox) can ignore our rename request outright, always reporting the
// uploader's own name instead, which is routinely differently formatted from
// (or an outright typo of) the release title and silently defeats any
// title-based match — so the magnet's hash is used directly whenever
// available; a non-magnet URL falls back to looking the hash up by title.
func (q *qbittorrent) addURLs(ctx context.Context, urls, title string) (string, error) {
	hash := magnetHash(urls)
	before := q.snapshotHashes(ctx)
	body, err := q.do(ctx, "/api/v2/torrents/add",
		url.Values{"urls": {urls}, "category": {q.cfg.Category}, "rename": {title}})
	if err != nil {
		// A debrid bridge can accept the magnet yet respond too slowly, tripping
		// the client timeout even though the torrent lands. Confirm via the list
		// before giving up, so the grab is still recorded.
		if hash != "" && q.hashLanded(ctx, hash) {
			return hash, nil
		}
		if h := q.findHash(title, before); h != "" {
			return h, nil
		}
		return "", err
	}
	if strings.HasPrefix(string(body), "Fails") {
		return "", fmt.Errorf("qbittorrent rejected the torrent")
	}
	if hash != "" {
		return hash, nil
	}
	return q.findHash(title, before), nil
}

// snapshotHashes returns the current set of torrent hashes in our category —
// a baseline findHash uses to tell the torrent just added apart from one
// already present under a similar title (an earlier volume of the same
// series already seeding: "Dune" already there, "Dune Messiah" just added).
// Returns nil (rather than an empty, non-nil map) on failure, which findHash
// treats as "no baseline" and falls back to matching without it.
func (q *qbittorrent) snapshotHashes(ctx context.Context) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	items, err := q.List(ctx)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[it.ID] = true
	}
	return seen
}

// hashLanded reports whether hash is now present in our category — used to
// confirm a magnet actually landed despite the add request itself erroring
// out (a slow debrid bridge tripping our client timeout).
func (q *qbittorrent) hashLanded(ctx context.Context, hash string) bool {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	items, err := q.List(ctx)
	if err != nil {
		return false
	}
	for _, it := range items {
		if strings.EqualFold(it.ID, hash) {
			return true
		}
	}
	return false
}

// findHash looks up the hash of the torrent matching title in our category,
// using a fresh short-lived context so a stalled add request doesn't taint the
// check. Hashes present in before (the pre-add snapshot) are skipped — a
// title match against one of those would misattribute an existing torrent to
// this brand new grab, corrupting both records (the queue would link the
// wrong book to it, and cancelling one grab could resolve the other's
// instead). An exact normalized-title match is preferred over a substring
// one, tolerating a tracker or the client itself mutating the name we asked
// for. Returns "" when nothing new matches.
func (q *qbittorrent) findHash(title string, before map[string]bool) string {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	items, err := q.List(ctx)
	if err != nil {
		return ""
	}
	want := normalizeTitle(title)
	if want == "" {
		return ""
	}
	fallback := ""
	for _, it := range items {
		if before[it.ID] {
			continue // present before the add — can't be what we just added
		}
		got := normalizeTitle(it.Title)
		if got == want {
			return it.ID
		}
		if fallback == "" && strings.Contains(got, want) {
			fallback = it.ID
		}
	}
	return fallback
}

// normalizeTitle lowercases a title to space-separated alphanumeric runs so
// cosmetic punctuation differences don't defeat the findHash() match.
func normalizeTitle(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// resolve follows a release's download URL to what the client actually needs:
// a magnet link (indexers redirect magnet-only results to one) or the raw
// .torrent bytes. It doesn't follow redirects automatically — a redirect to a
// magnet: scheme isn't an HTTP URL the client can follow — inspecting the
// Location header instead.
func (q *qbittorrent) resolve(ctx context.Context, dlURL string) (string, []byte, error) {
	client := &http.Client{
		Timeout:       60 * time.Second,
		Jar:           q.httpc.Jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cur := dlURL
	for i := 0; i < 5; i++ {
		if strings.HasPrefix(cur, "magnet:") {
			return cur, nil, nil
		}
		if !strings.HasPrefix(cur, "http://") && !strings.HasPrefix(cur, "https://") {
			return "", nil, fmt.Errorf("unsupported url scheme")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			return "", nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			// cur is the release's download URL — Newznab/Torznab convention
			// embeds the indexer's own apikey in it, so a failure here must
			// not surface (or later be logged) with that key intact.
			return "", nil, redact.URLError(err)
		}
		if loc := resp.Header.Get("Location"); resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != "" {
			resp.Body.Close()
			cur = loc
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		ct := resp.Header.Get("Content-Type")
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusOK {
			return "", nil, fmt.Errorf("resolving torrent: HTTP %d", status)
		}
		trimmed := bytes.TrimSpace(body)
		if bytes.HasPrefix(trimmed, []byte("magnet:")) {
			return string(trimmed), nil, nil
		}
		// A bencoded .torrent starts with a dictionary ('d') and names the
		// info/announce keys; some indexers omit the content-type.
		if strings.Contains(ct, "bittorrent") ||
			(len(trimmed) > 0 && trimmed[0] == 'd' &&
				(bytes.Contains(trimmed, []byte("4:info")) || bytes.Contains(trimmed, []byte("announce")))) {
			return "", body, nil
		}
		return "", nil, fmt.Errorf("response is neither a magnet nor a .torrent")
	}
	return "", nil, fmt.Errorf("too many redirects")
}

// addFile uploads .torrent bytes to qBittorrent (multipart torrents field), so
// a client that can't reach our indexer still gets the file.
func (q *qbittorrent) addFile(ctx context.Context, torrent []byte, title string) (string, error) {
	before := q.snapshotHashes(ctx)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("category", q.cfg.Category)
	if title != "" {
		_ = mw.WriteField("rename", title)
	}
	fw, err := mw.CreateFormFile("torrents", torrentFilename(title))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(torrent); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	payload := buf.Bytes()
	ct := mw.FormDataContentType()

	attempt := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			q.base()+"/api/v2/torrents/add", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", ct)
		return q.httpc.Do(req)
	}
	resp, err := attempt()
	if err != nil {
		// Slow bridge: the upload may have landed despite the timeout.
		if hash := q.findHash(title, before); hash != "" {
			return hash, nil
		}
		return "", fmt.Errorf("qbittorrent: %w", err)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if err := q.login(ctx); err != nil {
			return "", err
		}
		if resp, err = attempt(); err != nil {
			if hash := q.findHash(title, before); hash != "" {
				return hash, nil
			}
			return "", fmt.Errorf("qbittorrent: %w", err)
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qbittorrent: /api/v2/torrents/add: HTTP %d: %.100s", resp.StatusCode, body)
	}
	if strings.HasPrefix(string(body), "Fails") {
		return "", fmt.Errorf("qbittorrent rejected the torrent")
	}
	return q.findHash(title, before), nil
}

// torrentFilename makes a filesystem-safe "<title>.torrent" for the upload.
func torrentFilename(title string) string {
	safe := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r':
			return '_'
		}
		return r
	}, title)
	if safe == "" {
		safe = "download"
	}
	return safe + ".torrent"
}

func (q *qbittorrent) List(ctx context.Context) ([]Item, error) {
	// The client timeout is generous for slow adds; listing should stay snappy
	// so a hung bridge can't stall the periodic import loop.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	body, err := q.do(ctx, "/api/v2/torrents/info?category="+url.QueryEscape(q.cfg.Category), nil)
	if err != nil {
		return nil, err
	}
	var torrents []struct {
		Hash        string  `json:"hash"`
		Name        string  `json:"name"`
		State       string  `json:"state"`
		Progress    float64 `json:"progress"`
		ContentPath string  `json:"content_path"`
		SavePath    string  `json:"save_path"`
		Category    string  `json:"category"`
	}
	if err := json.Unmarshal(body, &torrents); err != nil {
		return nil, fmt.Errorf("qbittorrent: decoding torrent list: %w", err)
	}

	items := make([]Item, 0, len(torrents))
	for _, t := range torrents {
		if !strings.EqualFold(t.Category, q.cfg.Category) {
			// The category query param above is a server-side filter some
			// qBittorrent-compatible bridges (debrid services in particular)
			// don't actually honor, silently returning every app's torrents —
			// filter client-side too so another app's items sharing this
			// client never show up as ours.
			continue
		}
		item := Item{
			Client:   q.cfg.Name,
			ConfigID: q.cfg.ID,
			ID:       t.Hash,
			Title:    t.Name,
			Progress: t.Progress,
			Path:     t.ContentPath,
		}
		if item.Path == "" {
			item.Path = t.SavePath
		}
		item.Status = qbitStatus(t.State, t.Progress)
		items = append(items, item)
	}
	return items, nil
}

// qbitStatus normalizes qBittorrent's many states. Anything actively seeding
// or finished counts as completed — the file is on disk. A finished torrent
// that qBittorrent has paused/stopped (seed ratio or time goal reached, or
// paused by hand) reports "seeded": done seeding, safe to remove.
func qbitStatus(state string, progress float64) string {
	switch state {
	case "missingFiles":
		return "failed"
	case "error":
		// A finished torrent the client flags as errored still has its data on
		// disk — some debrid bridges mark a cached torrent "error" once it's
		// done downloading. Import it; only an error before completion is a
		// real failure.
		if progress >= 1 {
			return "completed"
		}
		return "failed"
	case "pausedDL", "stoppedDL":
		return "paused"
	case "queuedDL", "allocating", "metaDL", "checkingDL":
		return "queued"
	case "pausedUP", "stoppedUP":
		return "seeded"
	}
	if strings.HasSuffix(state, "UP") || progress >= 1 {
		return "completed"
	}
	return "downloading"
}

func (q *qbittorrent) Remove(ctx context.Context, id string, deleteData bool) error {
	del := "false"
	if deleteData {
		del = "true"
	}
	_, err := q.do(ctx, "/api/v2/torrents/delete",
		url.Values{"hashes": {id}, "deleteFiles": {del}})
	return err
}
