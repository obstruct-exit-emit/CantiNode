// Package hardcover implements metadata.Provider against the Hardcover.app
// GraphQL API (https://api.hardcover.app/v1/graphql, Bearer-token auth).
//
// The GraphQL queries follow Hardcover's Hasura schema and are verified
// against the live API; the response parsing stays deliberately defensive,
// and the query constants below are the single place to adjust if field
// names drift.
package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/librinode/librinode/internal/metadata"
)

const DefaultEndpoint = "https://api.hardcover.app/v1/graphql"

// Factory builds the provider for the metadata registry; an empty token
// reports the provider as not configured rather than failing.
func Factory(s metadata.Settings) (metadata.Provider, error) {
	if s.Token == "" {
		return nil, metadata.ErrNotConfigured
	}
	return New(s.Token,
		WithIncludeCompilations(s.IncludeCompilations),
		WithLanguage(s.Language),
	), nil
}

type Client struct {
	endpoint            string
	token               string
	httpc               *http.Client
	includeCompilations bool
	language            string // preferred metadata language ("" = no preference)
}

type Option func(*Client)

// WithEndpoint overrides the API endpoint (used by tests).
func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

// WithIncludeCompilations keeps box sets / omnibus editions in search results
// when true; the default (false) hides them so results are individual books.
func WithIncludeCompilations(v bool) Option {
	return func(c *Client) { c.includeCompilations = v }
}

// WithLanguage sets the preferred metadata language (e.g. "english"); an
// author's bibliography then drops foreign-language editions the author never
// wrote in that language. Empty ("" / "none") means no preference.
func WithLanguage(v string) Option {
	return func(c *Client) { c.language = v }
}

func New(token string, opts ...Option) *Client {
	c := &Client{
		endpoint: DefaultEndpoint,
		token:    token,
		httpc:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return "hardcover" }

// Validate checks the token against the live API using the `me` query, the
// cheapest authenticated call Hardcover offers.
func (c *Client) Validate(ctx context.Context) error {
	var out json.RawMessage
	return c.do(ctx, `query Validate { me { username } }`, nil, &out)
}

// --- GraphQL transport ---

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpc.Do(req)
	if err != nil {
		// The request never got a response — Hardcover (or the network path
		// to it) is down, not the token being wrong.
		return fmt.Errorf("hardcover: %w: %w", metadata.ErrUnreachable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("hardcover: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 5xx/429 are Hardcover's side acting up, not the token being wrong.
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("hardcover: %w: HTTP %d: %s", metadata.ErrUnreachable, resp.StatusCode, truncate(raw, 200))
		}
		return fmt.Errorf("hardcover: HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("hardcover: decoding response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("hardcover: graphql error: %s", envelope.Errors[0].Message)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("hardcover: decoding data: %w", err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// --- Search ---

// Hardcover's search endpoint proxies Typesense; `results` is a JSON blob
// whose hits[].document layout differs per query_type.
const searchQuery = `query Search($query: String!, $type: String!, $perPage: Int!) {
  search(query: $query, query_type: $type, per_page: $perPage, page: 1) {
    results
  }
}`

type searchEnvelope struct {
	Search struct {
		Results json.RawMessage `json:"results"`
	} `json:"search"`
}

type searchHits struct {
	Hits []struct {
		Document json.RawMessage `json:"document"`
	} `json:"hits"`
}

func (c *Client) search(ctx context.Context, query, queryType string) ([]json.RawMessage, error) {
	var env searchEnvelope
	err := c.do(ctx, searchQuery, map[string]any{
		"query": query, "type": queryType, "perPage": 25,
	}, &env)
	if err != nil {
		return nil, err
	}
	if len(env.Search.Results) == 0 {
		return nil, nil
	}
	// results may arrive as an object or as a JSON-encoded string of one.
	raw := env.Search.Results
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		raw = json.RawMessage(asString)
	}
	var hits searchHits
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("hardcover: decoding search results: %w", err)
	}
	docs := make([]json.RawMessage, 0, len(hits.Hits))
	for _, h := range hits.Hits {
		docs = append(docs, h.Document)
	}
	return docs, nil
}

func (c *Client) SearchAuthors(ctx context.Context, query string) ([]metadata.Author, error) {
	docs, err := c.search(ctx, query, "Author")
	if err != nil {
		return nil, err
	}
	authors := []metadata.Author{}
	for _, doc := range docs {
		var d struct {
			ID         flexID          `json:"id"`
			Name       string          `json:"name"`
			Image      json.RawMessage `json:"image"`
			BooksCount int             `json:"books_count"`
		}
		if err := json.Unmarshal(doc, &d); err != nil || d.ID == "" {
			continue // skip malformed hits rather than failing the search
		}
		authors = append(authors, metadata.Author{
			ForeignID: string(d.ID),
			Name:      d.Name,
			ImageURL:  imageURL(d.Image),
			BookCount: d.BooksCount,
		})
	}
	return authors, nil
}

// searchDominanceRatio drives search de-junking: within one normalized title, a
// record with fewer than 1/ratio the readers of the most-read same-title record
// is treated as a junk/ghost/foreign duplicate and dropped. Genuinely distinct
// same-title works — each with a real readership — both stay. Hardcover's search
// for "dune" otherwise returns Frank Herbert's Dune (13k readers) next to a film
// study, a French thesis, and two authorless ghost records all titled "Dune".
const searchDominanceRatio = 100

func (c *Client) SearchBooks(ctx context.Context, query string) ([]metadata.Book, error) {
	docs, err := c.search(ctx, query, "Book")
	if err != nil {
		return nil, err
	}

	type rec struct {
		book     metadata.Book
		titleKey string
		fullKey  string // title+author — collapses reissued/alternate editions
		users    int
	}
	recs := make([]rec, 0, len(docs))
	titleMax := map[string]int{} // most readers of any record sharing a title
	for _, doc := range docs {
		var d struct {
			ID          flexID          `json:"id"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Image       json.RawMessage `json:"image"`
			AuthorNames []string        `json:"author_names"`
			ReleaseDate string          `json:"release_date"`
			ReleaseYear int             `json:"release_year"`
			Rating      float64         `json:"rating"`
			UsersCount  int             `json:"users_count"`
			Compilation bool            `json:"compilation"`
		}
		if err := json.Unmarshal(doc, &d); err != nil || d.ID == "" {
			continue
		}
		// Box sets / omnibus editions are hidden unless the user opts in.
		if d.Compilation && !c.includeCompilations {
			continue
		}
		b := metadata.Book{
			ForeignID:   string(d.ID),
			Title:       d.Title,
			Description: d.Description,
			ReleaseDate: d.ReleaseDate,
			Rating:      d.Rating,
			CoverURL:    imageURL(d.Image),
		}
		if b.ReleaseDate == "" && d.ReleaseYear > 0 {
			b.ReleaseDate = strconv.Itoa(d.ReleaseYear)
		}
		author := ""
		if len(d.AuthorNames) > 0 {
			b.AuthorName = d.AuthorNames[0]
			author = d.AuthorNames[0]
		}
		tk := normalizeKey(d.Title)
		recs = append(recs, rec{
			book: b, titleKey: tk, fullKey: tk + "\x00" + normalizeKey(author), users: d.UsersCount,
		})
		if tk != "" && d.UsersCount > titleMax[tk] {
			titleMax[tk] = d.UsersCount
		}
	}

	// Drop never-read ghost records and same-title stragglers a dominant work
	// dwarfs; collapse exact title+author duplicates to their most-read record.
	// Relevance order is otherwise preserved. When every record of a title is
	// unread (titleMax 0), none are dropped — an obscure but real result survives.
	books := []metadata.Book{}
	at := map[string]int{}
	keptUsers := map[string]int{}
	for _, r := range recs {
		if m := titleMax[r.titleKey]; m > 0 {
			if r.users == 0 || r.users*searchDominanceRatio < m {
				continue
			}
		}
		if r.fullKey != "\x00" {
			if pos, ok := at[r.fullKey]; ok {
				if r.users > keptUsers[r.fullKey] {
					books[pos], keptUsers[r.fullKey] = r.book, r.users
				}
				continue
			}
			at[r.fullKey] = len(books)
			keptUsers[r.fullKey] = r.users
		}
		books = append(books, r.book)
	}
	return books, nil
}

// normalizeKey reduces a title or name to a comparison key: lower-cased, with
// runs of non-alphanumerics collapsed to single spaces and trimmed. So "DUNE",
// "Dune", and "Dune " map together while distinct works stay distinct.
func normalizeKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteRune(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// --- Author lookup ---

// Hardcover caps any selection at 100 rows, and prolific authors carry
// 1000+ contributions — every translation, box set, and reprint is one, and
// most have zero readers. An unordered fetch therefore returns 100 arbitrary
// rows (largely junk) and misses the canon entirely. Order by readership and
// skip never-read entries so the single 100-row page holds the canonical
// bibliography: the books people actually track, each with its description.
const authorQuery = `query Author($id: Int!, $lang: String!) {
  authors(where: {id: {_eq: $id}}, limit: 1) {
    id
    name
    bio
    cached_image
    contributions(
      where: {book: {id: {_is_null: false}, users_count: {_gte: 1}}},
      order_by: {book: {users_count: desc_nulls_last}},
      limit: 100
    ) {
      book {
        id
        title
        description
        release_date
        rating
        cached_image
        users_count
        compilation
        contributions_aggregate { aggregate { count } }
        lang_editions: editions(where: {language: {language: {_eq: $lang}}}, limit: 1) { id }
        foreign_editions: editions(where: {_and: [{language_id: {_is_null: false}}, {language: {language: {_neq: $lang}}}]}, limit: 1) { id }
        book_series {
          position
          series { id name description }
        }
      }
    }
  }
}`

type gqlSeriesEntry struct {
	Position float64 `json:"position"`
	Series   struct {
		ID          json.Number `json:"id"`
		Name        string      `json:"name"`
		Description string      `json:"description"`
	} `json:"series"`
}

type gqlBook struct {
	ID          json.Number      `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	ReleaseDate string           `json:"release_date"`
	Rating      float64          `json:"rating"`
	CachedImage json.RawMessage  `json:"cached_image"`
	BookSeries  []gqlSeriesEntry `json:"book_series"`
	// Bibliography-quality fields (author query only; absent elsewhere → zero).
	UsersCount      int               `json:"users_count"`
	Compilation     bool              `json:"compilation"`
	LangEditions    []json.RawMessage `json:"lang_editions"`    // one entry ⇒ has an edition in the preferred language
	ForeignEditions []json.RawMessage `json:"foreign_editions"` // one entry ⇒ has an edition in some other language
	Contributors    struct {
		Aggregate struct {
			Count int `json:"count"`
		} `json:"aggregate"`
	} `json:"contributions_aggregate"`
}

func (b *gqlBook) toMetadata() metadata.Book {
	out := metadata.Book{
		ForeignID:   b.ID.String(),
		Title:       b.Title,
		Description: b.Description,
		ReleaseDate: b.ReleaseDate,
		Rating:      b.Rating,
		CoverURL:    imageURL(b.CachedImage),
	}
	for _, se := range b.BookSeries {
		if se.Series.ID.String() == "" {
			continue
		}
		out.Series = append(out.Series, metadata.SeriesLink{
			ForeignID:   se.Series.ID.String(),
			Title:       se.Series.Name,
			Description: se.Series.Description,
			Position:    se.Position,
		})
	}
	return out
}

func (c *Client) GetAuthor(ctx context.Context, foreignID string) (*metadata.Author, error) {
	id, err := strconv.Atoi(foreignID)
	if err != nil {
		return nil, fmt.Errorf("hardcover: invalid author id %q: %w", foreignID, metadata.ErrNotFound)
	}

	var env struct {
		Authors []struct {
			ID            json.Number     `json:"id"`
			Name          string          `json:"name"`
			Bio           string          `json:"bio"`
			CachedImage   json.RawMessage `json:"cached_image"`
			Contributions []struct {
				Book *gqlBook `json:"book"`
			} `json:"contributions"`
		} `json:"authors"`
	}
	// $lang must be a value even with no preference (String!); the filter below
	// is simply skipped then. Hardcover names languages title-cased ("English").
	langParam := "English"
	if c.language != "" {
		langParam = titleCaseLang(c.language)
	}
	if err := c.do(ctx, authorQuery, map[string]any{"id": id, "lang": langParam}, &env); err != nil {
		return nil, err
	}
	if len(env.Authors) == 0 {
		return nil, metadata.ErrNotFound
	}

	a := env.Authors[0]
	author := &metadata.Author{
		ForeignID:   a.ID.String(),
		Name:        a.Name,
		Description: a.Bio,
		ImageURL:    imageURL(a.CachedImage),
	}
	// Dedup by book id, then by title. Hardcover lists a translation, reissue, or
	// alternate edition as its own book under the same author, so an author page
	// otherwise shows the same title several times. Contributions arrive ordered
	// by readership (see authorQuery), so the first record of a given title is the
	// canonical, most-read one — keep it and drop the rest.
	seenID := map[string]bool{}
	seenTitle := map[string]bool{}
	for _, con := range a.Contributions {
		b := con.Book
		if b == nil || seenID[b.ID.String()] {
			continue
		}
		seenID[b.ID.String()] = true

		// Box sets / omnibus editions are hidden unless the user opts in.
		if b.Compilation && !c.includeCompilations {
			continue
		}
		// Multi-author anthologies and graphic-novel adaptations (a magazine
		// issue, a "best of the year", a comics adaptation) are not the author's
		// own books — the author is one of many contributors. Real novels credit
		// one author, or a few co-authors; an anthology credits dozens. Drop the
		// crowded ones.
		if b.Contributors.Aggregate.Count >= anthologyContributors {
			continue
		}
		// Language: Hardcover lists every translation as its own book under the
		// same author, so a bibliography is mostly foreign-language editions of a
		// few works. With a language preference, keep a book only if it has an
		// edition in that language; otherwise drop it when it has a *known*
		// foreign edition, or too few readers to trust (Hardcover leaves many
		// editions' language blank, so a well-read book with no tagged language is
		// kept as a real work — an obscure English short — while the translations,
		// in any script, are dropped).
		if c.language != "" && len(b.LangEditions) == 0 {
			if len(b.ForeignEditions) > 0 || b.UsersCount < langFallbackReaders {
				continue
			}
		}
		if tkey := normalizeKey(b.Title); tkey != "" {
			if seenTitle[tkey] {
				continue
			}
			seenTitle[tkey] = true
		}
		book := b.toMetadata()
		book.AuthorForeignID = author.ForeignID
		book.AuthorName = author.Name
		author.Books = append(author.Books, book)
	}
	author.BookCount = len(author.Books)
	return author, nil
}

// langFallbackReaders keeps a book whose editions have no tagged copy in the
// preferred language but which enough people track that it's clearly a real
// work (Hardcover leaves many editions' language blank).
const langFallbackReaders = 5

// anthologyContributors is the author-credit count at which a book is treated as
// a multi-author anthology / adaptation rather than the author's own work. Real
// novels credit one author or a few co-authors; anthologies credit many.
const anthologyContributors = 5

// titleCaseLang upper-cases a language name's first letter to match Hardcover's
// stored form ("english" → "English").
func titleCaseLang(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- Book lookup ---

const bookQuery = `query Book($id: Int!) {
  books(where: {id: {_eq: $id}}, limit: 1) {
    id
    title
    description
    release_date
    rating
    cached_image
    contributions {
      author { id name }
    }
    book_series {
      position
      series { id name description }
    }
    editions {
      id
      title
      isbn_13
      asin
      release_date
      reading_format_id
      cached_image
      publisher { name }
      language { language }
    }
  }
}`

// Hardcover reading_format_id values (per its public schema docs):
// 1 = physical book, 2 = audiobook, 4 = ebook.
func editionFormat(readingFormatID int) string {
	switch readingFormatID {
	case 1:
		return "physical"
	case 2:
		return "audiobook"
	case 4:
		return "ebook"
	default:
		return "unknown"
	}
}

func (c *Client) GetBook(ctx context.Context, foreignID string) (*metadata.Book, error) {
	id, err := strconv.Atoi(foreignID)
	if err != nil {
		return nil, fmt.Errorf("hardcover: invalid book id %q: %w", foreignID, metadata.ErrNotFound)
	}

	var env struct {
		Books []struct {
			gqlBook
			Contributions []struct {
				Author struct {
					ID   json.Number `json:"id"`
					Name string      `json:"name"`
				} `json:"author"`
			} `json:"contributions"`
			Editions []struct {
				ID              json.Number     `json:"id"`
				Title           string          `json:"title"`
				ISBN13          string          `json:"isbn_13"`
				ASIN            string          `json:"asin"`
				ReleaseDate     string          `json:"release_date"`
				ReadingFormatID int             `json:"reading_format_id"`
				CachedImage     json.RawMessage `json:"cached_image"`
				Publisher       *struct {
					Name string `json:"name"`
				} `json:"publisher"`
				Language *struct {
					Language string `json:"language"`
				} `json:"language"`
			} `json:"editions"`
		} `json:"books"`
	}
	if err := c.do(ctx, bookQuery, map[string]any{"id": id}, &env); err != nil {
		return nil, err
	}
	if len(env.Books) == 0 {
		return nil, metadata.ErrNotFound
	}

	raw := env.Books[0]
	book := raw.gqlBook.toMetadata()
	if len(raw.Contributions) > 0 {
		book.AuthorForeignID = raw.Contributions[0].Author.ID.String()
		book.AuthorName = raw.Contributions[0].Author.Name
	}
	for _, ed := range raw.Editions {
		e := metadata.Edition{
			ForeignID:   ed.ID.String(),
			Title:       ed.Title,
			ISBN13:      ed.ISBN13,
			ASIN:        ed.ASIN,
			Format:      editionFormat(ed.ReadingFormatID),
			ReleaseDate: ed.ReleaseDate,
			CoverURL:    imageURL(ed.CachedImage),
		}
		if ed.Publisher != nil {
			e.Publisher = ed.Publisher.Name
		}
		if ed.Language != nil {
			e.Language = ed.Language.Language
		}
		book.Editions = append(book.Editions, e)
	}
	return &book, nil
}

// --- JSON helpers ---

// flexID accepts a JSON number or string id and normalizes it to a string.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexID(n.String())
		return nil
	}
	return fmt.Errorf("id is neither string nor number: %s", b)
}

// imageURL extracts a URL from Hardcover's image fields, which may be a
// plain string, an object like {"url": "..."}, or null.
func imageURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.URL
	}
	return ""
}
