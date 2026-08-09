package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/redact"
)

// Client speaks the Newznab/Torznab API. One instance serves all indexers;
// per-indexer settings are passed to each call.
type Client struct {
	httpc *http.Client
}

func NewClient() *Client {
	return &Client{httpc: &http.Client{Timeout: 60 * time.Second}}
}

// apiURL builds <base>/api?t=<fn>&apikey=...&<params>. Indexer base URLs may
// be given with or without the trailing /api.
func apiURL(ind *Indexer, fn string, params url.Values) string {
	base := strings.TrimRight(ind.BaseURL, "/")
	if !strings.HasSuffix(base, "/api") {
		base += "/api"
	}
	params.Set("t", fn)
	if ind.APIKey != "" {
		params.Set("apikey", ind.APIKey)
	}
	return base + "?" + params.Encode()
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	// The indexer's own API key rides in the query string (apiURL above) —
	// captured up front so it can be scrubbed from anything that echoes the
	// request back (a failed-connection error embeds the URL verbatim; some
	// indexers' error pages restate the query string in the body).
	secrets := redact.Values(rawURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LibriNode")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, redact.URLError(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %.150s", resp.StatusCode, redact.Text(string(body), secrets))
	}
	return body, nil
}

// nzbError is the Newznab error document: <error code="100" description=.../>
type nzbError struct {
	XMLName     xml.Name `xml:"error"`
	Code        string   `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

func checkAPIError(body []byte) error {
	var e nzbError
	if err := xml.Unmarshal(body, &e); err == nil && e.XMLName.Local == "error" {
		return fmt.Errorf("indexer error %s: %s", e.Code, e.Description)
	}
	return nil
}

// Test fetches the indexer's capabilities document, verifying the URL and
// API key work.
func (c *Client) Test(ctx context.Context, ind *Indexer) error {
	body, err := c.get(ctx, apiURL(ind, "caps", url.Values{}))
	if err != nil {
		return err
	}
	if err := checkAPIError(body); err != nil {
		return err
	}
	var caps struct {
		XMLName xml.Name `xml:"caps"`
	}
	if err := xml.Unmarshal(body, &caps); err != nil {
		return fmt.Errorf("unexpected caps response: %w", err)
	}
	return nil
}

// rss mirrors the Newznab/Torznab search response. encoding/xml matches
// unqualified names in any namespace, so <newznab:attr> and <torznab:attr>
// both land in Attrs.
type rss struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Comments  string `xml:"comments"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

// Search runs a free-text query limited to the given categories (pass the
// media type's list via Indexer.CategoriesFor).
func (c *Client) Search(ctx context.Context, ind *Indexer, query, categories string) ([]Release, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", "100")
	if cats := strings.TrimSpace(categories); cats != "" {
		params.Set("cat", cats)
	}

	body, err := c.get(ctx, apiURL(ind, "search", params))
	if err != nil {
		return nil, err
	}
	if err := checkAPIError(body); err != nil {
		return nil, err
	}

	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	releases := make([]Release, 0, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		releases = append(releases, itemToRelease(ind, item))
	}
	return releases, nil
}

func itemToRelease(ind *Indexer, item rssItem) Release {
	r := Release{
		IndexerID:   ind.ID,
		Indexer:     ind.Name,
		Protocol:    ind.Protocol(),
		Title:       item.Title,
		GUID:        item.GUID,
		InfoURL:     item.Comments,
		DownloadURL: item.Link,
		PublishDate: normalizeDate(item.PubDate),
		Seeders:     -1,
		Peers:       -1,
	}
	if r.DownloadURL == "" {
		r.DownloadURL = item.Enclosure.URL
	}
	if n, err := strconv.ParseInt(item.Enclosure.Length, 10, 64); err == nil && n > 0 {
		r.Size = n
	}
	for _, a := range item.Attrs {
		switch a.Name {
		case "size":
			if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && r.Size == 0 {
				r.Size = n
			}
		case "category":
			if n, err := strconv.Atoi(a.Value); err == nil {
				r.Categories = append(r.Categories, n)
			}
		case "seeders":
			if n, err := strconv.Atoi(a.Value); err == nil {
				r.Seeders = n
			}
		case "peers":
			if n, err := strconv.Atoi(a.Value); err == nil {
				r.Peers = n
			}
		}
	}
	return r
}

// normalizeDate converts RSS pub dates to RFC 3339, passing through anything
// it can't parse.
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}
