// Package comicvine implements metadata.SeriesProvider for comics against
// the ComicVine REST API (https://comicvine.gamespot.com/api, free API key
// required; "volume" in ComicVine terms is a comic series).
package comicvine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/metadata"
	"github.com/librinode/librinode/internal/redact"
)

const DefaultEndpoint = "https://comicvine.gamespot.com/api"

// Factory builds the provider; an empty key reports not-configured.
func Factory(s metadata.Settings) (metadata.SeriesProvider, error) {
	if s.Token == "" {
		return nil, metadata.ErrNotConfigured
	}
	return New(s.Token), nil
}

type Client struct {
	endpoint string
	apiKey   string
	httpc    *http.Client
}

type Option func(*Client)

func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		endpoint: DefaultEndpoint,
		apiKey:   apiKey,
		httpc:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string      { return "comicvine" }
func (c *Client) MediaType() string { return "comic" }

// Validate checks the API key against the live API with the cheapest
// authenticated call available — a 1-result volume listing.
func (c *Client) Validate(ctx context.Context) error {
	var out json.RawMessage
	return c.get(ctx, "/volumes/", url.Values{"limit": {"1"}, "field_list": {"id"}}, &out)
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")
	endpoint := strings.TrimRight(c.endpoint, "/") + path + "?" + params.Encode()
	// Captured before the request so a failure (which embeds the URL
	// verbatim) or an echoing error body can be scrubbed of the API key.
	secrets := redact.Values(endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	// ComicVine blocks default Go/curl user agents.
	req.Header.Set("User-Agent", "LibriNode")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("comicvine: %w: %w", metadata.ErrUnreachable, redact.URLError(err))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("comicvine: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body := redact.Text(string(raw), secrets)
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("comicvine: %w: HTTP %d: %.150s", metadata.ErrUnreachable, resp.StatusCode, body)
		}
		return fmt.Errorf("comicvine: HTTP %d: %.150s", resp.StatusCode, body)
	}
	var envelope struct {
		StatusCode int             `json:"status_code"` // 1 = OK
		Error      string          `json:"error"`
		Results    json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("comicvine: decoding response: %w", err)
	}
	switch envelope.StatusCode {
	case 1: // OK
	case 101: // object not found
		return metadata.ErrNotFound
	case 102: // API rate limit exceeded
		return fmt.Errorf("comicvine: %w: %s (code %d)", metadata.ErrUnreachable, envelope.Error, envelope.StatusCode)
	default:
		return fmt.Errorf("comicvine: %s (code %d)", envelope.Error, envelope.StatusCode)
	}
	return json.Unmarshal(envelope.Results, out)
}

// cvVolume is ComicVine's volume (= comic series) shape.
type cvVolume struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StartYear   string `json:"start_year"`
	IssueCount  int    `json:"count_of_issues"`
	Image       struct {
		MediumURL string `json:"medium_url"`
	} `json:"image"`
	PersonCredits []struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"person_credits"`
	Issues []struct {
		ID          int    `json:"id"`
		IssueNumber string `json:"issue_number"`
		Name        string `json:"name"`
	} `json:"issues"`
}

var htmlTags = regexp.MustCompile(`<[^>]+>`)

func (v *cvVolume) toResult() metadata.SeriesResult {
	year, _ := strconv.Atoi(v.StartYear)
	author := ""
	for _, p := range v.PersonCredits {
		if strings.Contains(strings.ToLower(p.Role), "writer") {
			author = p.Name
			break
		}
	}
	desc := htmlTags.ReplaceAllString(v.Description, "")
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	return metadata.SeriesResult{
		ForeignID:   strconv.Itoa(v.ID),
		Title:       v.Name,
		Description: strings.TrimSpace(desc),
		AuthorName:  author,
		Year:        year,
		CoverURL:    v.Image.MediumURL,
		IssueCount:  v.IssueCount,
	}
}

func (c *Client) SearchSeries(ctx context.Context, query string) ([]metadata.SeriesResult, error) {
	var volumes []cvVolume
	params := url.Values{
		"resources": {"volume"},
		"query":     {query},
		"limit":     {"20"},
	}
	if err := c.get(ctx, "/search/", params, &volumes); err != nil {
		return nil, err
	}
	results := make([]metadata.SeriesResult, 0, len(volumes))
	for i := range volumes {
		results = append(results, volumes[i].toResult())
	}
	return results, nil
}

func (c *Client) GetSeries(ctx context.Context, foreignID string) (*metadata.SeriesResult, error) {
	if _, err := strconv.Atoi(foreignID); err != nil {
		return nil, fmt.Errorf("comicvine: invalid id %q: %w", foreignID, metadata.ErrNotFound)
	}
	var volume cvVolume
	// 4050 is ComicVine's type prefix for volumes.
	if err := c.get(ctx, "/volume/4050-"+foreignID+"/", url.Values{}, &volume); err != nil {
		return nil, err
	}
	result := volume.toResult()
	for _, issue := range volume.Issues {
		number, err := strconv.ParseFloat(issue.IssueNumber, 64)
		if err != nil {
			continue // annuals and specials with odd numbering are skipped
		}
		result.Issues = append(result.Issues, metadata.Issue{
			ForeignID: strconv.Itoa(issue.ID),
			Number:    number,
			Title:     issue.Name,
		})
	}
	return &result, nil
}
