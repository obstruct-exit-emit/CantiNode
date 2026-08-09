package download

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/librinode/librinode/internal/redact"
)

// The direct client is LibriNode's own downloader — a third protocol beside
// torrent and usenet for sources that hand out plain HTTP file links (Anna's
// Archive fast-download, Libgen mirrors, open-book collections). There is no
// external program: Add streams the file into the configured download folder
// itself, Completed Download Handling imports the result like any other
// finished download, and Remove deletes it.
//
// A release's download URL may carry several mirrors separated by "|" — they
// are tried in order until one delivers. A fetched body that turns out to be
// JSON with a "download_url" field (the shape membership APIs like Anna's
// fast_download.json answer with) is followed one hop to the real file.
//
// The in-flight queue is in-memory: a restart forgets active downloads (the
// files on disk keep whatever bytes arrived; the importer's orphan sweep
// resolves their grabs). Completed items survive as files and are re-listed
// until removed.

const (
	// directTimeout bounds one download end to end.
	directTimeout = 2 * time.Hour
	// directUA is sent on every request; some file hosts refuse blank agents.
	directUA = "LibriNode"
)

type directItem struct {
	id       string
	title    string
	status   string // queued | downloading | completed | failed
	progress float64
	path     string
	cancel   context.CancelFunc
	err      string
}

type direct struct {
	cfg   *ClientConfig
	httpc *http.Client

	mu    sync.Mutex
	items map[string]*directItem
}

func newDirect(cfg *ClientConfig) *direct {
	// No overall client timeout — downloads are long; each download's context
	// carries its own deadline instead.
	return &direct{cfg: cfg, httpc: &http.Client{}, items: map[string]*directItem{}}
}

// dir is the configured download folder (stored in the config's Host field —
// the direct client has no host; the UI labels the field accordingly).
func (d *direct) dir() string { return strings.TrimSpace(d.cfg.Host) }

// Test verifies the download folder exists (creating it if needed) and is
// writable.
func (d *direct) Test(ctx context.Context) error {
	dir := d.dir()
	if dir == "" {
		return fmt.Errorf("no download folder configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating download folder: %w", err)
	}
	probe := filepath.Join(dir, ".librinode-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("download folder not writable: %w", err)
	}
	os.Remove(probe)
	return nil
}

// Add starts downloading the release; the returned id identifies it in List/
// Remove. The download itself runs in the background — Add returns as soon as
// it's underway, like handing a URL to an external client would.
func (d *direct) Add(ctx context.Context, rawURL, title string) (string, error) {
	mirrors := splitMirrors(rawURL)
	if len(mirrors) == 0 {
		return "", fmt.Errorf("release has no download URL (the source may need a membership/API key for downloads)")
	}
	if err := d.Test(ctx); err != nil {
		return "", err
	}

	id := randomID()
	// Independent of the request context: the HTTP request that triggered the
	// grab finishes long before the download does.
	dctx, cancel := context.WithTimeout(context.Background(), directTimeout)
	item := &directItem{id: id, title: title, status: "queued", cancel: cancel}
	d.mu.Lock()
	d.items[id] = item
	d.mu.Unlock()

	go d.run(dctx, item, mirrors)
	return id, nil
}

// run tries each mirror in order until one delivers the file.
func (d *direct) run(ctx context.Context, item *directItem, mirrors []string) {
	defer item.cancel()
	var lastErr error
	for _, m := range mirrors {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		d.setStatus(item, "downloading", "")
		path, err := d.download(ctx, item, m)
		if err == nil {
			d.mu.Lock()
			item.path = path
			item.status = "completed"
			item.progress = 1
			d.mu.Unlock()
			return
		}
		lastErr = err
	}
	msg := "download failed"
	if lastErr != nil {
		msg = redact.URLError(lastErr).Error()
	}
	d.setStatus(item, "failed", msg)
}

func (d *direct) setStatus(item *directItem, status, errMsg string) {
	d.mu.Lock()
	item.status = status
	item.err = errMsg
	d.mu.Unlock()
}

// download fetches one URL into the folder, following up to two indirection
// hops to the real file: a JSON "download_url" envelope (membership
// fast-download APIs) or an HTML landing page naming the file (open-mirror
// hosts put the direct link behind a "GET" page).
func (d *direct) download(ctx context.Context, item *directItem, rawURL string) (string, error) {
	resp, err := d.get(ctx, rawURL)
	if err != nil {
		return "", err
	}

	for hop := 0; hop < 2; hop++ {
		ct := resp.Header.Get("Content-Type")
		switch {
		case strings.Contains(ct, "json"):
			// A JSON answer isn't the file — an API envelope naming it.
			var envelope struct {
				DownloadURL string `json:"download_url"`
				Error       string `json:"error"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope)
			resp.Body.Close()
			if decodeErr != nil {
				return "", fmt.Errorf("unexpected JSON answer: %w", decodeErr)
			}
			if envelope.DownloadURL == "" {
				if envelope.Error != "" {
					return "", fmt.Errorf("download API: %s", envelope.Error)
				}
				return "", fmt.Errorf("download API answered without a download_url")
			}
			if resp, err = d.get(ctx, envelope.DownloadURL); err != nil {
				return "", err
			}
			continue
		case strings.Contains(ct, "text/html"):
			// A landing page, not the file: pick the download link out of it.
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			resp.Body.Close()
			if readErr != nil {
				return "", readErr
			}
			next := fileLinkFromHTML(string(body), resp.Request.URL)
			if next == "" {
				return "", fmt.Errorf("mirror page has no recognizable download link")
			}
			if resp, err = d.get(ctx, next); err != nil {
				return "", err
			}
			continue
		}
		break
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") || strings.Contains(ct, "json") {
		return "", fmt.Errorf("mirror kept answering with pages instead of a file")
	}

	// Peek the leading bytes: they sniff the file's true type (mirror URLs end in
	// get.php and the Content-Type is often a generic octet-stream, so neither
	// reliably names the file) and catch an error/landing page a mirror served AS
	// a file — one delivered with an octet-stream type slips past the guard above.
	header := make([]byte, 512)
	hn, herr := io.ReadFull(resp.Body, header)
	if herr != nil && herr != io.EOF && herr != io.ErrUnexpectedEOF {
		return "", herr
	}
	header = header[:hn]
	if looksLikeWebPage(header) {
		return "", fmt.Errorf("mirror delivered a web page, not the file")
	}

	dest := filepath.Join(d.dir(), safeFilename(item.title)+extensionFor(resp, header))
	part := dest + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}

	total := resp.ContentLength
	var written int64
	// The peeked header is served ahead of the rest so no bytes are lost.
	body := io.MultiReader(bytes.NewReader(header), resp.Body)
	buf := make([]byte, 128<<10)
	for {
		if ctx.Err() != nil {
			f.Close()
			os.Remove(part)
			return "", ctx.Err()
		}
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", werr
			}
			written += int64(n)
			if total > 0 {
				d.mu.Lock()
				item.progress = min(float64(written)/float64(total), 0.999)
				d.mu.Unlock()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			return "", rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return "", err
	}
	if written == 0 {
		os.Remove(part)
		return "", fmt.Errorf("mirror delivered an empty file")
	}
	if err := os.Rename(part, dest); err != nil {
		os.Remove(part)
		return "", err
	}
	return dest, nil
}

func (d *direct) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", directUA)
	resp, err := d.httpc.Do(req)
	if err != nil {
		return nil, redact.URLError(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d from mirror", resp.StatusCode)
	}
	return resp, nil
}

// List reports the tracked downloads (this client's own queue).
func (d *direct) List(ctx context.Context) ([]Item, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	items := make([]Item, 0, len(d.items))
	for _, it := range d.items {
		items = append(items, Item{
			Client:   d.cfg.Name,
			ConfigID: d.cfg.ID,
			ID:       it.id,
			Title:    it.title,
			Status:   it.status,
			Progress: it.progress,
			Path:     it.path,
		})
	}
	return items, nil
}

// Remove cancels/forgets a download, optionally deleting its file.
func (d *direct) Remove(ctx context.Context, id string, deleteData bool) error {
	d.mu.Lock()
	it, ok := d.items[id]
	if ok {
		delete(d.items, id)
	}
	d.mu.Unlock()
	if !ok {
		return nil // already gone — removing twice is fine
	}
	it.cancel()
	if deleteData && it.path != "" {
		os.Remove(it.path)
	}
	return nil
}

// anchorRe captures each <a href="…">text</a> pair on a landing page.
var anchorRe = regexp.MustCompile(`(?is)<a\s+[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)

// htmlTagRe strips markup from an anchor's inner text.
var htmlTagRe = regexp.MustCompile(`(?s)<[^>]+>`)

// fileExtRe marks hrefs whose path ends in a downloadable book/audio format.
var fileExtRe = regexp.MustCompile(`(?i)\.(epub|pdf|mobi|azw3?|cbz|cbr|djvu|m4b|m4a|mp3|flac|zip)([?#]|$)`)

// fileLinkFromHTML picks the download link out of a mirror landing page. The
// open-mirror hosts share a small set of shapes: a big "GET" anchor
// (library.lol, libgen ads pages), a get.php-style href, a direct file href,
// or an IPFS gateway link — tried in that order. Returns "" when nothing on
// the page looks like a file.
func fileLinkFromHTML(page string, base *url.URL) string {
	type link struct{ href, text string }
	links := []link{}
	for _, m := range anchorRe.FindAllStringSubmatch(page, -1) {
		text := strings.TrimSpace(htmlTagRe.ReplaceAllString(m[2], " "))
		links = append(links, link{href: m[1], text: strings.ToUpper(strings.Join(strings.Fields(text), " "))})
	}
	pick := func(match func(link) bool) string {
		for _, l := range links {
			if match(l) {
				if abs := absoluteURL(base, l.href); abs != "" {
					return abs
				}
			}
		}
		return ""
	}
	if u := pick(func(l link) bool { return l.text == "GET" }); u != "" {
		return u
	}
	if u := pick(func(l link) bool { return strings.Contains(strings.ToLower(l.href), "get.php") }); u != "" {
		return u
	}
	if u := pick(func(l link) bool { return fileExtRe.MatchString(l.href) }); u != "" {
		return u
	}
	return pick(func(l link) bool { return strings.Contains(strings.ToLower(l.href), "ipfs") })
}

// absoluteURL resolves a possibly-relative href against the page's URL.
func absoluteURL(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "#") {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if base == nil {
		if ref.IsAbs() {
			return ref.String()
		}
		return ""
	}
	return base.ResolveReference(ref).String()
}

// splitMirrors splits a "url|url|url" mirror list, dropping empties.
func splitMirrors(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, "|") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// safeFilename reduces a release title to a filesystem-safe base name.
func safeFilename(title string) string {
	title = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = "download"
	}
	if len(title) > 150 {
		title = title[:150]
	}
	return title
}

// extensionFor picks the downloaded file's extension, most-reliable signal
// first: the content sniff (what the bytes actually are), then the
// Content-Disposition filename, then the URL path, then the Content-Type. The
// last three are allow-listed to real media types so a file served from a
// get.php/download.aspx link is never saved as ".php" — the bug that made a
// perfectly good ebook unimportable.
func extensionFor(resp *http.Response, header []byte) string {
	if ext := sniffExt(header); ext != "" {
		return ext
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if i := strings.LastIndex(cd, "filename="); i >= 0 {
			name := strings.Trim(strings.TrimSpace(cd[i+len("filename="):]), `"'`)
			if ext := path.Ext(name); mediaExt(ext) {
				return strings.ToLower(ext)
			}
		}
	}
	if ext := path.Ext(resp.Request.URL.Path); mediaExt(ext) {
		return strings.ToLower(ext)
	}
	switch ct := resp.Header.Get("Content-Type"); {
	case strings.Contains(ct, "epub"):
		return ".epub"
	case strings.Contains(ct, "pdf"):
		return ".pdf"
	case strings.Contains(ct, "mobi"):
		return ".mobi"
	}
	return ".bin" // unknown — the importer will name what it expected instead
}

// sniffExt identifies a file from its leading magic bytes — the one signal a
// mirror can't misreport. Covers the book formats whose signatures are
// unambiguous; anything else falls through to the header/URL heuristics.
func sniffExt(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return ".pdf"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		// A ZIP container. Only epub (an OCF zip whose first stored entry is the
		// "mimetype" file) can be named for certain; a bare zip could be cbz/cbr
		// and is left to the other signals.
		if bytes.Contains(b, []byte("application/epub+zip")) {
			return ".epub"
		}
	case len(b) >= 68 && bytes.Contains(b[60:68], []byte("BOOKMOBI")):
		// PalmDB header: the type/creator at offset 60 is "BOOKMOBI" for
		// MOBI and the KF8/azw3 files that carry a MOBI-compatible header.
		return ".mobi"
	}
	return ""
}

// looksLikeWebPage reports whether a downloaded body is really an HTML/XML error
// or landing page rather than a book file. No format LibriNode accepts begins
// with '<', so a leading angle bracket (after optional whitespace or a BOM) is a
// reliable tell — and it catches pages a mirror serves as octet-stream, which
// the Content-Type guard misses.
func looksLikeWebPage(b []byte) bool {
	b = bytes.TrimLeft(b, " \t\r\n\uFEFF")
	return len(b) > 0 && b[0] == '<'
}

// mediaExt allow-lists the real book/comic/audio file extensions the direct
// client may save — so a URL or filename ending in a script/page extension
// (.php, .aspx, .cgi) never becomes the saved file's type.
func mediaExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".epub", ".pdf", ".mobi", ".azw", ".azw3", ".fb2", ".djvu", ".txt", ".rtf",
		".cbz", ".cbr", ".zip", ".m4b", ".m4a", ".mp3", ".flac", ".opus":
		return true
	}
	return false
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "direct-" + hex.EncodeToString(b)
}
