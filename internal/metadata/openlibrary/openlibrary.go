// Package openlibrary implements metadata.Provider against the Open Library
// REST API (https://openlibrary.org). It is keyless and public, which makes
// it a natural fallback for books the primary provider doesn't carry.
//
// Open Library models a book as a "work" (OL…W) with separate "editions"
// (OL…M) and "authors" (OL…A); this provider uses the work key as the book
// foreign id and the author key as the author foreign id, both without the
// leading "/works/" or "/authors/" path segment.
package openlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/librinode/librinode/internal/metadata"
)

const (
	DefaultBaseURL  = "https://openlibrary.org"
	coversBaseURL   = "https://covers.openlibrary.org/b/id"
	providerName    = "openlibrary"
	searchLimit     = 25
	bibliographyCap = 50
)

// Factory builds the provider for the metadata registry. Open Library needs
// no credentials, so it is always configured (the token, if any, is ignored).
func Factory(_ metadata.Settings) (metadata.Provider, error) {
	return New(), nil
}

type Client struct {
	baseURL string
	httpc   *http.Client
}

type Option func(*Client)

// WithBaseURL overrides the API base (used by tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

func New(opts ...Option) *Client {
	c := &Client{
		baseURL: DefaultBaseURL,
		httpc:   &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return providerName }

// Validate does a cheap public search; a transport failure is unreachable,
// anything that returns is proof the API is up (there is no token to reject).
// The query term must be a real word: Open Library now rejects a stopword-only
// query (e.g. "the") with HTTP 422, which would fail validation spuriously.
func (c *Client) Validate(ctx context.Context) error {
	var out struct {
		NumFound int `json:"numFound"`
	}
	return c.getJSON(ctx, "/search.json?q=book&limit=1&fields=key", &out)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	// Open Library asks clients to identify themselves with an app name and a
	// contact — an identified client gets triple the rate limit (3 req/s vs 1).
	req.Header.Set("User-Agent", "LibriNode/1.0 (+https://github.com/obstruct-exit-emit/LibriNode)")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("openlibrary: %w: %w", metadata.ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("openlibrary: reading response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return metadata.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("openlibrary: %w: HTTP %d", metadata.ErrUnreachable, resp.StatusCode)
		}
		return fmt.Errorf("openlibrary: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("openlibrary: decoding response: %w", err)
	}
	return nil
}

// olKey strips Open Library's path prefix ("/works/OL…W" → "OL…W"), leaving a
// bare key usable as a foreign id.
func olKey(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return path
	}
	return path[i+1:]
}

func coverURL(coverID int) string {
	if coverID <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d-L.jpg", coversBaseURL, coverID)
}

// --- Search ---

type searchResponse struct {
	Docs []struct {
		Key              string   `json:"key"`
		Title            string   `json:"title"`
		AuthorName       []string `json:"author_name"`
		AuthorKey        []string `json:"author_key"`
		FirstPublishYear int      `json:"first_publish_year"`
		CoverID          int      `json:"cover_i"`
		RatingsAverage   float64  `json:"ratings_average"`
	} `json:"docs"`
}

func (c *Client) SearchBooks(ctx context.Context, query string) ([]metadata.Book, error) {
	path := "/search.json?limit=" + strconv.Itoa(searchLimit) +
		"&fields=key,title,author_name,author_key,first_publish_year,cover_i,ratings_average" +
		"&q=" + url.QueryEscape(query)
	var resp searchResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, err
	}
	books := make([]metadata.Book, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		if d.Key == "" || d.Title == "" {
			continue
		}
		b := metadata.Book{
			ForeignID: olKey(d.Key),
			Title:     d.Title,
			Rating:    d.RatingsAverage,
			CoverURL:  coverURL(d.CoverID),
			Source:    providerName,
		}
		if d.FirstPublishYear > 0 {
			b.ReleaseDate = strconv.Itoa(d.FirstPublishYear)
		}
		if len(d.AuthorName) > 0 {
			b.AuthorName = d.AuthorName[0]
		}
		if len(d.AuthorKey) > 0 {
			b.AuthorForeignID = d.AuthorKey[0]
		}
		books = append(books, b)
	}
	return books, nil
}

type authorSearchResponse struct {
	Docs []struct {
		Key       string `json:"key"` // bare "OL…A" here, no path prefix
		Name      string `json:"name"`
		WorkCount int    `json:"work_count"`
	} `json:"docs"`
}

func (c *Client) SearchAuthors(ctx context.Context, query string) ([]metadata.Author, error) {
	path := "/search/authors.json?limit=" + strconv.Itoa(searchLimit) +
		"&q=" + url.QueryEscape(query)
	var resp authorSearchResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, err
	}
	authors := make([]metadata.Author, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		if d.Key == "" || d.Name == "" {
			continue
		}
		authors = append(authors, metadata.Author{
			ForeignID: olKey(d.Key),
			Name:      d.Name,
			BookCount: d.WorkCount,
			Source:    providerName,
		})
	}
	return authors, nil
}

// --- Description decoding ---

// olText is Open Library's recurring "string or {value: string}" shape for
// descriptions and bios.
type olText string

func (t *olText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = olText(s)
		return nil
	}
	var obj struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(b, &obj); err == nil {
		*t = olText(obj.Value)
		return nil
	}
	return nil // unknown shape → empty, never fail the whole record
}

// --- Book lookup ---

func (c *Client) GetBook(ctx context.Context, foreignID string) (*metadata.Book, error) {
	var work struct {
		Key         string `json:"key"`
		Title       string `json:"title"`
		Description olText `json:"description"`
		Covers      []int  `json:"covers"`
		Authors     []struct {
			Author struct {
				Key string `json:"key"`
			} `json:"author"`
		} `json:"authors"`
	}
	if err := c.getJSON(ctx, "/works/"+url.PathEscape(foreignID)+".json", &work); err != nil {
		return nil, err
	}
	if work.Title == "" {
		return nil, metadata.ErrNotFound
	}

	book := metadata.Book{
		ForeignID:   foreignID,
		Title:       work.Title,
		Description: string(work.Description),
		Source:      providerName,
	}
	if len(work.Covers) > 0 {
		book.CoverURL = coverURL(work.Covers[0])
	}
	if len(work.Authors) > 0 {
		authorKey := olKey(work.Authors[0].Author.Key)
		book.AuthorForeignID = authorKey
		if a, err := c.getAuthorRecord(ctx, authorKey); err == nil {
			book.AuthorName = a.Name
		}
	}
	book.Editions = c.editionsFor(ctx, foreignID)
	return &book, nil
}

// editionsFor pulls a work's editions for their ISBNs and publishers; a
// failure here is non-fatal (the book is still usable without editions).
func (c *Client) editionsFor(ctx context.Context, workID string) []metadata.Edition {
	var resp struct {
		Entries []struct {
			Key         string   `json:"key"`
			Title       string   `json:"title"`
			ISBN13      []string `json:"isbn_13"`
			Publishers  []string `json:"publishers"`
			PublishDate string   `json:"publish_date"`
			Covers      []int    `json:"covers"`
			Languages   []struct {
				Key string `json:"key"` // "/languages/eng"
			} `json:"languages"`
		} `json:"entries"`
	}
	if err := c.getJSON(ctx, "/works/"+url.PathEscape(workID)+"/editions.json", &resp); err != nil {
		return nil
	}
	editions := make([]metadata.Edition, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		ed := metadata.Edition{
			ForeignID:   olKey(e.Key),
			Title:       e.Title,
			Format:      "unknown",
			ReleaseDate: e.PublishDate,
		}
		if len(e.ISBN13) > 0 {
			ed.ISBN13 = e.ISBN13[0]
		}
		if len(e.Publishers) > 0 {
			ed.Publisher = e.Publishers[0]
		}
		if len(e.Covers) > 0 {
			ed.CoverURL = coverURL(e.Covers[0])
		}
		if len(e.Languages) > 0 {
			ed.Language = olKey(e.Languages[0].Key)
		}
		editions = append(editions, ed)
		if len(editions) >= searchLimit {
			break
		}
	}
	return editions
}

// --- Author lookup ---

type authorRecord struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Bio    olText `json:"bio"`
	Photos []int  `json:"photos"`
}

func (c *Client) getAuthorRecord(ctx context.Context, foreignID string) (*authorRecord, error) {
	var a authorRecord
	if err := c.getJSON(ctx, "/authors/"+url.PathEscape(foreignID)+".json", &a); err != nil {
		return nil, err
	}
	if a.Name == "" {
		return nil, metadata.ErrNotFound
	}
	return &a, nil
}

func (c *Client) GetAuthor(ctx context.Context, foreignID string) (*metadata.Author, error) {
	rec, err := c.getAuthorRecord(ctx, foreignID)
	if err != nil {
		return nil, err
	}
	author := &metadata.Author{
		ForeignID:   foreignID,
		Name:        rec.Name,
		Description: string(rec.Bio),
		Source:      providerName,
	}
	if len(rec.Photos) > 0 && rec.Photos[0] > 0 {
		author.ImageURL = fmt.Sprintf("https://covers.openlibrary.org/a/id/%d-L.jpg", rec.Photos[0])
	}

	var works struct {
		Entries []struct {
			Key              string `json:"key"`
			Title            string `json:"title"`
			Description      olText `json:"description"`
			Covers           []int  `json:"covers"`
			FirstPublishDate string `json:"first_publish_date"`
		} `json:"entries"`
	}
	if err := c.getJSON(ctx, "/authors/"+url.PathEscape(foreignID)+"/works.json?limit="+
		strconv.Itoa(bibliographyCap), &works); err == nil {
		for _, w := range works.Entries {
			if w.Title == "" {
				continue
			}
			b := metadata.Book{
				ForeignID:       olKey(w.Key),
				Title:           w.Title,
				Description:     string(w.Description),
				ReleaseDate:     w.FirstPublishDate,
				AuthorForeignID: foreignID,
				AuthorName:      rec.Name,
				Source:          providerName,
			}
			if len(w.Covers) > 0 {
				b.CoverURL = coverURL(w.Covers[0])
			}
			author.Books = append(author.Books, b)
		}
	}
	author.BookCount = len(author.Books)
	return author, nil
}
