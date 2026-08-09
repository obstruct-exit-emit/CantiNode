// Package googlebooks implements metadata.Provider against the Google Books
// API (https://www.googleapis.com/books/v1). It works without a key (subject
// to Google's anonymous rate limits); an optional API key raises those
// limits and is passed through the provider's token setting.
//
// Google Books is volume-centric and has no first-class author entity, so it
// is a strong book-search fallback but a weak author source: GetAuthor
// synthesizes an author from an inauthor: volume search. The volume id is the
// book foreign id; a slugified author name is the author foreign id.
package googlebooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/metadata"
)

const (
	DefaultBaseURL = "https://www.googleapis.com/books/v1"
	providerName   = "googlebooks"
	searchLimit    = 25
	authorIDPrefix = "author:" // author foreign ids are "author:<lowercased name>"
)

// Factory builds the provider for the metadata registry. The key is optional
// (keyless access works within Google's anonymous limits), so the provider is
// always configured.
func Factory(s metadata.Settings) (metadata.Provider, error) {
	return New(s.Token), nil
}

type Client struct {
	baseURL string
	apiKey  string
	httpc   *http.Client
}

type Option func(*Client)

// WithBaseURL overrides the API base (used by tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		apiKey:  apiKey,
		httpc:   &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return providerName }

// Validate does a cheap search; a transport failure is unreachable, a 4xx
// (other than 404) means the key was supplied and rejected.
func (c *Client) Validate(ctx context.Context) error {
	var out volumesResponse
	return c.getJSON(ctx, "/volumes?maxResults=1&q="+url.QueryEscape("the"), &out)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	full := c.baseURL + path
	if c.apiKey != "" {
		sep := "&"
		if !strings.Contains(full, "?") {
			sep = "?"
		}
		full += sep + "key=" + url.QueryEscape(c.apiKey)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("googlebooks: %w: %w", metadata.ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("googlebooks: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		// Keyless access shares one global anonymous daily quota that is often
		// already exhausted; the fix is a (free) API key for a private quota.
		// Say so, instead of a bare "HTTP 429", so the cause is actionable.
		if resp.StatusCode == http.StatusTooManyRequests && c.apiKey == "" {
			return fmt.Errorf("googlebooks: %w: shared anonymous daily quota exhausted — "+
				"add a free Google Books API key (Settings → Metadata) for your own quota",
				metadata.ErrUnreachable)
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("googlebooks: %w: HTTP %d", metadata.ErrUnreachable, resp.StatusCode)
		}
		return fmt.Errorf("googlebooks: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("googlebooks: decoding response: %w", err)
	}
	return nil
}

// --- Volume shapes ---

type volume struct {
	ID         string `json:"id"`
	VolumeInfo struct {
		Title         string   `json:"title"`
		Subtitle      string   `json:"subtitle"`
		Authors       []string `json:"authors"`
		Description   string   `json:"description"`
		PublishedDate string   `json:"publishedDate"`
		Publisher     string   `json:"publisher"`
		Language      string   `json:"language"`
		AverageRating float64  `json:"averageRating"`
		ImageLinks    struct {
			Thumbnail      string `json:"thumbnail"`
			SmallThumbnail string `json:"smallThumbnail"`
		} `json:"imageLinks"`
		IndustryIdentifiers []struct {
			Type       string `json:"type"`
			Identifier string `json:"identifier"`
		} `json:"industryIdentifiers"`
	} `json:"volumeInfo"`
}

type volumesResponse struct {
	Items []volume `json:"items"`
}

func (v *volume) coverURL() string {
	if v.VolumeInfo.ImageLinks.Thumbnail != "" {
		return httpsify(v.VolumeInfo.ImageLinks.Thumbnail)
	}
	return httpsify(v.VolumeInfo.ImageLinks.SmallThumbnail)
}

// httpsify upgrades Google's http image links so they don't get blocked as
// mixed content behind an HTTPS proxy.
func httpsify(u string) string {
	if strings.HasPrefix(u, "http://") {
		return "https://" + u[len("http://"):]
	}
	return u
}

func (v *volume) isbn13() string {
	for _, id := range v.VolumeInfo.IndustryIdentifiers {
		if id.Type == "ISBN_13" {
			return id.Identifier
		}
	}
	return ""
}

func authorID(name string) string {
	return authorIDPrefix + strings.ToLower(strings.TrimSpace(name))
}

func (v *volume) toBook() metadata.Book {
	title := v.VolumeInfo.Title
	if v.VolumeInfo.Subtitle != "" {
		title += ": " + v.VolumeInfo.Subtitle
	}
	b := metadata.Book{
		ForeignID:   v.ID,
		Title:       title,
		Description: v.VolumeInfo.Description,
		ReleaseDate: v.VolumeInfo.PublishedDate,
		Rating:      v.VolumeInfo.AverageRating,
		CoverURL:    v.coverURL(),
		Source:      providerName,
	}
	if len(v.VolumeInfo.Authors) > 0 {
		b.AuthorName = v.VolumeInfo.Authors[0]
		b.AuthorForeignID = authorID(v.VolumeInfo.Authors[0])
	}
	if isbn := v.isbn13(); isbn != "" || v.VolumeInfo.Publisher != "" {
		b.Editions = []metadata.Edition{{
			ForeignID:   v.ID,
			Title:       title,
			ISBN13:      isbn,
			Format:      "unknown",
			Publisher:   v.VolumeInfo.Publisher,
			Language:    v.VolumeInfo.Language,
			ReleaseDate: v.VolumeInfo.PublishedDate,
			CoverURL:    b.CoverURL,
		}}
	}
	return b
}

// --- Search ---

func (c *Client) searchVolumes(ctx context.Context, q string) ([]volume, error) {
	path := fmt.Sprintf("/volumes?maxResults=%d&printType=books&q=%s", searchLimit, url.QueryEscape(q))
	var resp volumesResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) SearchBooks(ctx context.Context, query string) ([]metadata.Book, error) {
	items, err := c.searchVolumes(ctx, query)
	if err != nil {
		return nil, err
	}
	books := make([]metadata.Book, 0, len(items))
	for i := range items {
		if items[i].VolumeInfo.Title == "" {
			continue
		}
		books = append(books, items[i].toBook())
	}
	return books, nil
}

// SearchAuthors synthesizes authors from the distinct author names appearing
// in a title search — Google Books has no author entity of its own.
func (c *Client) SearchAuthors(ctx context.Context, query string) ([]metadata.Author, error) {
	items, err := c.searchVolumes(ctx, query)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	authors := []metadata.Author{}
	for i := range items {
		for _, name := range items[i].VolumeInfo.Authors {
			id := authorID(name)
			if seen[id] {
				continue
			}
			seen[id] = true
			authors = append(authors, metadata.Author{
				ForeignID: id,
				Name:      name,
				Source:    providerName,
			})
		}
	}
	return authors, nil
}

// --- Lookup ---

func (c *Client) GetBook(ctx context.Context, foreignID string) (*metadata.Book, error) {
	// A synthesized author id is not a volume id — reject it cleanly so the
	// fallback chain moves on instead of hitting the API with garbage.
	if strings.HasPrefix(foreignID, authorIDPrefix) {
		return nil, metadata.ErrNotFound
	}
	var v volume
	if err := c.getJSON(ctx, "/volumes/"+url.PathEscape(foreignID), &v); err != nil {
		return nil, err
	}
	if v.VolumeInfo.Title == "" {
		return nil, metadata.ErrNotFound
	}
	b := v.toBook()
	return &b, nil
}

// GetAuthor reconstructs an author from an inauthor: search. The foreign id is
// the "author:<name>" form SearchBooks/SearchAuthors emit; a raw name is also
// accepted so a stub author refresh still resolves.
func (c *Client) GetAuthor(ctx context.Context, foreignID string) (*metadata.Author, error) {
	name := strings.TrimPrefix(foreignID, authorIDPrefix)
	if name == "" {
		return nil, metadata.ErrNotFound
	}
	items, err := c.searchVolumes(ctx, `inauthor:"`+name+`"`)
	if err != nil {
		return nil, err
	}
	// Prefer the provider's own casing of the name from a matching volume.
	displayName := name
	author := &metadata.Author{
		ForeignID: authorID(name),
		Source:    providerName,
	}
	for i := range items {
		v := &items[i]
		if v.VolumeInfo.Title == "" || !authorMatches(v.VolumeInfo.Authors, name) {
			continue
		}
		if displayName == name && len(v.VolumeInfo.Authors) > 0 {
			displayName = v.VolumeInfo.Authors[0]
		}
		b := v.toBook()
		b.AuthorForeignID = author.ForeignID
		author.Books = append(author.Books, b)
	}
	if len(author.Books) == 0 {
		return nil, metadata.ErrNotFound
	}
	author.Name = displayName
	author.BookCount = len(author.Books)
	return author, nil
}

func authorMatches(authors []string, name string) bool {
	for _, a := range authors {
		if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}
