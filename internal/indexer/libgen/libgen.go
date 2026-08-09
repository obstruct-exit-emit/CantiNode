// Package libgen is a native indexer for Library Genesis: the long-running
// open ebook mirror network. It has no Newznab API, so LibriNode scrapes its
// search (both the non-fiction and fiction indexes) and builds direct-protocol
// releases whose download URLs point at the open mirror hosts, keyed by the
// file's MD5 — the identifier every Libgen mirror serves by. The direct
// download client follows each mirror's landing page to the real file and
// fails over between mirrors.
//
// This is a dual-use shadow-library source: it is never bundled or enabled by
// default; a user adds it deliberately and is responsible for its use. HTML
// selectors target Libgen's known layout and may need updating when the site
// changes — the inherent fragility of a scraped source.
package libgen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/librinode/librinode/internal/indexer"
)

const (
	// Name is both the registry key and the stored indexer type.
	Name = "libgen"
	// DefaultBaseURL is libgen.li — the live fork at time of writing (the older
	// libgen.is/rs mirrors are frequently down). The site runs several domains,
	// so the indexer's site URLs can override it.
	DefaultBaseURL = "https://libgen.li"

	maxResults = 100
	userAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
)

// MirrorDownloadURLs builds the "|"-separated direct-protocol download URL for
// a file known by MD5: the open mirror hosts that serve Libgen content, tried
// in order by the direct client (each is a landing page it knows how to follow
// — libgen.li's ads.php leads to a get.php link). Shared with Anna's Archive,
// whose downloads-by-MD5 resolve through the same mirror.
func MirrorDownloadURLs(md5 string) string {
	return "https://libgen.li/ads.php?md5=" + strings.ToLower(md5)
}

// Def is the native-indexer definition; register it with indexer.RegisterNative.
func Def() indexer.NativeDef {
	return indexer.NativeDef{
		Name:           Name,
		DisplayName:    "Library Genesis",
		Protocol:       indexer.ProtocolDirect,
		MediaTypes:     []string{"ebook"},
		DefaultBaseURL: DefaultBaseURL,
		WIP:            true,
		New: func(ind *indexer.Indexer, httpc *http.Client) indexer.Searcher {
			return &searcher{ind: ind, bases: parseBases(ind.BaseURL), httpc: httpc}
		},
	}
}

func parseBases(raw string) []string {
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
	bases []string
	httpc *http.Client
}

// Test confirms the site answers on at least one configured URL.
func (s *searcher) Test(ctx context.Context) error {
	var err error
	for _, base := range s.bases {
		if _, err = s.fetch(ctx, base+"/"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no configured site URL answered (tried %d): %w", len(s.bases), err)
}

// Search queries libgen.li's combined index (books + fiction) on the first
// configured site URL that answers.
func (s *searcher) Search(ctx context.Context, query, mediaType string) ([]indexer.Release, error) {
	if mediaType != "ebook" {
		return nil, nil
	}
	var base, page string
	var err error
	for _, b := range s.bases {
		page, err = s.fetch(ctx, b+"/index.php?req="+url.QueryEscape(query)+"&res=100")
		if err == nil {
			base = b
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if base == "" {
		return nil, fmt.Errorf("no configured site URL answered (tried %d): %w", len(s.bases), err)
	}

	seen := map[string]bool{}
	releases := []indexer.Release{}
	for _, res := range parseResults(page) {
		if len(releases) >= maxResults {
			break
		}
		if seen[res.MD5] {
			continue
		}
		seen[res.MD5] = true
		releases = append(releases, indexer.Release{
			IndexerID: s.ind.ID,
			Indexer:   s.ind.Name,
			Protocol:  indexer.ProtocolDirect,
			// A scene-like name so the shared release scorer can do its job:
			// the author (for the author check), the title, the year, the
			// language (so a wrong-language edition is rejected), and the file
			// format (so it's kept only when the quality profile wants it).
			Title:   res.releaseName(),
			GUID:    res.MD5,
			InfoURL: base + "/file.php?md5=" + res.MD5,
			// ads.php on the serving host → get.php → file (the direct client
			// follows the landing page).
			DownloadURL: base + "/ads.php?md5=" + res.MD5,
			Size:        res.Size,
			Seeders:     -1,
			Peers:       -1,
		})
	}
	return releases, nil
}

func (s *searcher) fetch(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}
	return string(body), nil
}

// --- Parsing (pure functions; fixture-tested) ---

type result struct {
	MD5      string
	Title    string
	Authors  string // "Last, First; …" as libgen.li lists them
	Year     string
	Language string
	Format   string // file extension: epub, pdf, fb2, …
	Size     int64
}

// releaseName renders a result as a scene-like release name the shared scorer
// understands: "Author - Title (Year) language ext". The author lets the
// book-match's author check pass, the language lets a wrong-language edition be
// rejected, and the extension lets the quality profile keep only wanted formats.
func (r result) releaseName() string {
	name := r.Title
	if r.Authors != "" {
		name = r.Authors + " - " + r.Title
	}
	if r.Year != "" {
		name += " (" + r.Year + ")"
	}
	if r.Language != "" {
		name += " " + r.Language
	}
	if r.Format != "" {
		name += " " + r.Format
	}
	return name
}

var (
	// A result row's md5 rides its /ads.php?md5=<hash> download link.
	md5Re = regexp.MustCompile(`(?i)md5=([0-9a-f]{32})`)
	// Results are table rows.
	rowRe = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	// The title is the row's first edition.php link text ("Hunters of Dune").
	// Anchored on href= (not the opening <a) because libgen.li's anchors carry
	// a title="…<br>" attribute whose literal '>' would otherwise truncate an
	// [^>]* attribute scan before it reaches href.
	titleRe = regexp.MustCompile(`(?is)href="edition\.php[^"]*"[^>]*>(.*?)</a>`)
	cellRe  = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	tagRe   = regexp.MustCompile(`(?s)<[^>]+>`)
	// Sizes render like "990 kB", "1.2 MB" in the file.php cell.
	sizeRe = regexp.MustCompile(`(?i)\b([0-9][0-9.,]*)\s*(kb|mb|gb)\b`)
	// Per-cell classifiers for the language, format, and year columns.
	langCell = regexp.MustCompile(`(?i)^(english|german|french|spanish|italian|dutch|russian|portuguese|polish|japanese|chinese|korean|swedish|norwegian|danish|finnish|czech|greek|turkish|arabic|hindi|latin|hungarian|romanian|ukrainian)$`)
	extCell  = regexp.MustCompile(`(?i)^(epub|pdf|mobi|azw3?|fb2|djvu?|txt|rtf|lit|doc|docx|cbz|cbr)$`)
	yearCell = regexp.MustCompile(`^(1[4-9]\d\d|20\d\d)$`)
)

// parseResults extracts one result per libgen.li table row. The columns are
// title, Author(s), Publisher, Year, Language, Pages, Size, Ext, Mirrors — so
// the author is the plain <td> immediately after the title cell. (libgen.li no
// longer wraps authors in author.php links, so there is nothing to match on but
// column position — an author never reaches the release name otherwise, and the
// scorer then rejects every result for "not mentioning the author".)
func parseResults(page string) []result {
	out := []result{}
	for _, row := range rowRe.FindAllStringSubmatch(page, -1) {
		block := row[1]
		m := md5Re.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		cells := cellRe.FindAllStringSubmatch(block, -1)

		// The title is the first cell carrying an edition.php link.
		titleIdx, title := -1, ""
		for i, c := range cells {
			if t := titleRe.FindStringSubmatch(c[1]); t != nil {
				if title = cleanText(t[1]); title != "" {
					titleIdx = i
					break
				}
			}
		}
		if title == "" {
			continue
		}
		res := result{MD5: strings.ToLower(m[1]), Title: title, Size: parseSize(block)}

		// Author(s): the cell right after the title cell.
		if titleIdx+1 < len(cells) {
			res.Authors = cleanAuthor(cells[titleIdx+1][1])
		}

		// Year, language, and format each live in their own single-value cell —
		// skip the title and author cells so their free text can't be misread as
		// one of these.
		for i, c := range cells {
			if i == titleIdx || i == titleIdx+1 {
				continue
			}
			txt := cleanText(c[1])
			switch {
			case res.Year == "" && yearCell.MatchString(txt):
				res.Year = txt
			case res.Language == "" && langCell.MatchString(txt):
				res.Language = strings.ToLower(txt)
			case res.Format == "" && extCell.MatchString(txt):
				res.Format = strings.ToLower(txt)
			}
		}
		out = append(out, res)
	}
	return out
}

// cleanAuthor strips libgen.li's "(Author)" role suffix from an author name.
func cleanAuthor(s string) string {
	name := cleanText(s)
	name = strings.TrimSpace(strings.TrimSuffix(name, "(Author)"))
	return strings.TrimSpace(strings.TrimSuffix(name, " (Author)"))
}

func parseSize(block string) int64 {
	m := sizeRe.FindStringSubmatch(tagRe.ReplaceAllString(block, " "))
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
	}
	return int64(n)
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(tagRe.ReplaceAllString(s, " ")), " ")
}
