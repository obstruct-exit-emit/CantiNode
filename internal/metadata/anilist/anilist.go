// Package anilist implements metadata.SeriesProvider for manga against the
// AniList GraphQL API (https://graphql.anilist.co — public, no key needed).
//
// AniList carries series-level data (title, staff, volume count, covers);
// individual volumes have no per-volume records, so GetSeries synthesizes
// Issues 1..volumes. Ongoing series often report no volume count yet — they
// gain volumes on later refreshes as AniList updates.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/metadata"
)

const DefaultEndpoint = "https://graphql.anilist.co"

// Factory registers AniList; it needs no key, so it is always configured.
// The global metadata preferences ride along: language picks the title style
// (English vs romaji), includeAdult gates adult-flagged search results.
func Factory(s metadata.Settings) (metadata.SeriesProvider, error) {
	c := New()
	c.preferRomaji = s.Language != "" && !strings.Contains(strings.ToLower(s.Language), "english")
	c.includeAdult = s.IncludeAdult
	return c, nil
}

type Client struct {
	endpoint string
	httpc    *http.Client
	// Global metadata preferences (see Factory).
	preferRomaji bool
	includeAdult bool
}

type Option func(*Client)

func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

func New(opts ...Option) *Client {
	c := &Client{
		endpoint: DefaultEndpoint,
		httpc:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string      { return "anilist" }
func (c *Client) MediaType() string { return "manga" }

// Validate checks connectivity to AniList — there's no key to reject (the
// API is public), so this only ever reports the endpoint being unreachable.
func (c *Client) Validate(ctx context.Context) error {
	var out json.RawMessage
	return c.do(ctx, `query { __typename }`, nil, &out)
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("anilist: %w: %w", metadata.ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("anilist: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("anilist: %w: HTTP %d: %.150s", metadata.ErrUnreachable, resp.StatusCode, raw)
		}
		return fmt.Errorf("anilist: HTTP %d: %.150s", resp.StatusCode, raw)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("anilist: decoding response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		if envelope.Errors[0].Message == "Not Found." {
			return metadata.ErrNotFound
		}
		return fmt.Errorf("anilist: %s", envelope.Errors[0].Message)
	}
	return json.Unmarshal(envelope.Data, out)
}

// gqlMedia is the Media shape shared by search and lookup.
type gqlMedia struct {
	ID      int  `json:"id"`
	IsAdult bool `json:"isAdult"`
	Title   struct {
		English string `json:"english"`
		Romaji  string `json:"romaji"`
	} `json:"title"`
	Description string `json:"description"`
	Volumes     int    `json:"volumes"`
	StartDate   struct {
		Year int `json:"year"`
	} `json:"startDate"`
	CoverImage struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	Staff struct {
		Edges []struct {
			Role string `json:"role"`
			Node struct {
				Name struct {
					Full string `json:"full"`
				} `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"staff"`
}

const mediaFields = `
	id
	isAdult
	title { english romaji }
	description(asHtml: false)
	volumes
	startDate { year }
	coverImage { large }
	staff(perPage: 4) { edges { role node { name { full } } } }`

var htmlTags = regexp.MustCompile(`<[^>]+>`)

func (m *gqlMedia) toResult(preferRomaji bool) metadata.SeriesResult {
	title := m.Title.English
	if preferRomaji || title == "" {
		if m.Title.Romaji != "" {
			title = m.Title.Romaji
		}
	}
	author := ""
	for _, edge := range m.Staff.Edges {
		// "Story & Art", "Story" — the mangaka role starts with Story.
		if len(edge.Role) >= 5 && edge.Role[:5] == "Story" {
			author = edge.Node.Name.Full
			break
		}
	}
	return metadata.SeriesResult{
		ForeignID:   strconv.Itoa(m.ID),
		Title:       title,
		Description: htmlTags.ReplaceAllString(m.Description, ""),
		AuthorName:  author,
		Year:        m.StartDate.Year,
		CoverURL:    m.CoverImage.Large,
		IssueCount:  m.Volumes,
	}
}

func (c *Client) SearchSeries(ctx context.Context, query string) ([]metadata.SeriesResult, error) {
	var out struct {
		Page struct {
			Media []gqlMedia `json:"media"`
		} `json:"Page"`
	}
	q := `query ($search: String) { Page(perPage: 20) {
		media(search: $search, type: MANGA, format_in: [MANGA, ONE_SHOT]) {` + mediaFields + `}
	} }`
	if err := c.do(ctx, q, map[string]any{"search": query}, &out); err != nil {
		return nil, err
	}
	results := make([]metadata.SeriesResult, 0, len(out.Page.Media))
	for i := range out.Page.Media {
		// Adult-flagged series stay out of search results unless the global
		// include-adult preference is on.
		if out.Page.Media[i].IsAdult && !c.includeAdult {
			continue
		}
		results = append(results, out.Page.Media[i].toResult(c.preferRomaji))
	}
	return results, nil
}

func (c *Client) GetSeries(ctx context.Context, foreignID string) (*metadata.SeriesResult, error) {
	id, err := strconv.Atoi(foreignID)
	if err != nil {
		return nil, fmt.Errorf("anilist: invalid id %q: %w", foreignID, metadata.ErrNotFound)
	}
	var out struct {
		Media *gqlMedia `json:"Media"`
	}
	q := `query ($id: Int) { Media(id: $id, type: MANGA) {` + mediaFields + `} }`
	if err := c.do(ctx, q, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.Media == nil {
		return nil, metadata.ErrNotFound
	}
	result := out.Media.toResult(c.preferRomaji)
	// AniList has no per-volume records; synthesize Vol. 1..N.
	for i := 1; i <= result.IssueCount; i++ {
		result.Issues = append(result.Issues, metadata.Issue{
			ForeignID: fmt.Sprintf("%s-v%d", result.ForeignID, i),
			Number:    float64(i),
			Title:     fmt.Sprintf("Vol. %d", i),
		})
	}
	return &result, nil
}
