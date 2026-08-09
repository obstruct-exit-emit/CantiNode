package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/librinode/librinode/internal/metadata"
)

// mockAPI serves canned GraphQL responses keyed by operation name and
// verifies auth + request shape on every call.
func mockAPI(t *testing.T, responses map[string]string, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		for op, resp := range responses {
			if len(req.Query) >= len("query "+op) && req.Query[:len("query "+op)] == "query "+op {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(resp))
				return
			}
		}
		t.Errorf("no canned response for query: %.60s", req.Query)
		http.Error(w, "unexpected query", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return New("test-token", append([]Option{WithEndpoint(srv.URL)}, opts...)...)
}

func TestSearchAuthors(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Search": `{"data": {"search": {"results": {
			"hits": [
				{"document": {"id": "123", "name": "Terry Pratchett", "books_count": 41,
					"image": {"url": "https://img.example/tp.jpg"}}},
				{"document": {"id": 456, "name": "Neil Gaiman", "books_count": 30, "image": null}}
			]
		}}}}`,
	})

	authors, err := c.SearchAuthors(context.Background(), "pratchett")
	if err != nil {
		t.Fatalf("SearchAuthors: %v", err)
	}
	if len(authors) != 2 {
		t.Fatalf("got %d authors, want 2", len(authors))
	}
	if authors[0].ForeignID != "123" || authors[0].Name != "Terry Pratchett" {
		t.Errorf("author[0] = %+v", authors[0])
	}
	if authors[0].ImageURL != "https://img.example/tp.jpg" {
		t.Errorf("image url = %q", authors[0].ImageURL)
	}
	if authors[0].BookCount != 41 {
		t.Errorf("book count = %d", authors[0].BookCount)
	}
	// Numeric id normalized to string.
	if authors[1].ForeignID != "456" {
		t.Errorf("author[1] id = %q, want 456", authors[1].ForeignID)
	}
}

func TestSearchBooks_ResultsAsJSONString(t *testing.T) {
	// The Typesense payload can arrive as a JSON-encoded string; ensure we
	// unwrap it.
	inner := `{"hits": [{"document": {"id": 99, "title": "Mort", "author_names": ["Terry Pratchett"], "release_year": 1987, "rating": 4.25, "image": {"url": "https://img.example/mort.jpg"}}}]}`
	payload, _ := json.Marshal(inner)
	c := mockAPI(t, map[string]string{
		"Search": `{"data": {"search": {"results": ` + string(payload) + `}}}`,
	})

	books, err := c.SearchBooks(context.Background(), "mort")
	if err != nil {
		t.Fatalf("SearchBooks: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("got %d books, want 1", len(books))
	}
	b := books[0]
	if b.ForeignID != "99" || b.Title != "Mort" || b.AuthorName != "Terry Pratchett" {
		t.Errorf("book = %+v", b)
	}
	if b.ReleaseDate != "1987" {
		t.Errorf("release date = %q, want 1987 (from release_year)", b.ReleaseDate)
	}
	if b.Rating != 4.25 {
		t.Errorf("rating = %v", b.Rating)
	}
}

// TestSearchBooksDeJunks: Hardcover returns one canonical record next to
// low/zero-reader same-title junk (a film study and a ghost record both titled
// "Dune") and a true edition duplicate. Search keeps the canonical works only,
// in relevance order.
func TestSearchBooksDeJunks(t *testing.T) {
	inner := `{"hits": [
		{"document": {"id": 1, "title": "Dune", "author_names": ["Frank Herbert"], "users_count": 13575, "release_year": 1965}},
		{"document": {"id": 2, "title": "Dune", "author_names": ["Christian McCrea"], "users_count": 4, "release_year": 2019}},
		{"document": {"id": 3, "title": "Dune", "author_names": [], "users_count": 0}},
		{"document": {"id": 4, "title": "DUNE", "author_names": ["Mahalia Galais"], "users_count": 0, "release_year": 2019}},
		{"document": {"id": 5, "title": "Mistborn", "author_names": ["Brandon Sanderson"], "users_count": 9000}},
		{"document": {"id": 6, "title": "Mistborn", "author_names": ["Brandon Sanderson"], "users_count": 200}},
		{"document": {"id": 7, "title": "Dune Messiah", "author_names": ["Frank Herbert"], "users_count": 4501}}
	]}`
	c := mockAPI(t, map[string]string{
		"Search": `{"data": {"search": {"results": ` + inner + `}}}`,
	})
	books, err := c.SearchBooks(context.Background(), "dune")
	if err != nil {
		t.Fatalf("SearchBooks: %v", err)
	}
	got := make([]string, len(books))
	for i, b := range books {
		got[i] = b.ForeignID
	}
	// Kept: canonical Dune (1), most-read Mistborn (5), Dune Messiah (7).
	// Dropped: same-title stragglers/ghosts 2,3,4 and duplicate edition 6.
	want := []string{"1", "5", "7"}
	if len(got) != len(want) {
		t.Fatalf("got ids %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got ids %v, want %v", got, want)
		}
	}
}

// TestSearchBooksCompilations: box sets are hidden by default and shown when
// the user opts in.
func TestSearchBooksCompilations(t *testing.T) {
	inner := `{"hits": [
		{"document": {"id": 1, "title": "Dune", "author_names": ["Frank Herbert"], "users_count": 13575}},
		{"document": {"id": 2, "title": "The Great Dune Trilogy", "author_names": ["Frank Herbert"], "users_count": 272, "compilation": true}}
	]}`
	resp := map[string]string{"Search": `{"data": {"search": {"results": ` + inner + `}}}`}

	def := mockAPI(t, resp) // default: compilations hidden
	books, err := def.SearchBooks(context.Background(), "dune")
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].ForeignID != "1" {
		t.Fatalf("default should hide the box set: got %d books", len(books))
	}

	opted := mockAPI(t, resp, WithIncludeCompilations(true))
	all, err := opted.SearchBooks(context.Background(), "dune")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("opt-in should include the box set: got %d books, want 2", len(all))
	}
}

// TestGetAuthorDeJunks: with a language preference, an author's foreign-language
// editions and box sets are dropped, but real works are kept — even ones with no
// tagged edition in that language, as long as enough people track them.
func TestGetAuthorDeJunks(t *testing.T) {
	book := func(id int, title string, users, contrib int, comp, hasLang, hasForeign bool) string {
		langEd, frnEd := "[]", "[]"
		if hasLang {
			langEd = `[{"id": 1}]`
		}
		if hasForeign {
			frnEd = `[{"id": 2}]`
		}
		return fmt.Sprintf(`{"book": {"id": %d, "title": %q, "users_count": %d, "compilation": %t,
			"contributions_aggregate": {"aggregate": {"count": %d}}, "lang_editions": %s, "foreign_editions": %s}}`,
			id, title, users, comp, contrib, langEd, frnEd)
	}
	resp := map[string]string{
		"Author": `{"data": {"authors": [{"id": 1, "name": "A", "bio": "", "cached_image": null, "contributions": [` +
			book(10, "The Martian", 9925, 1, false, true, true) + `,` + // kept: English edition
			book(11, "Марсианин", 2, 1, false, false, false) + `,` + // dropped: no lang, few readers
			book(12, "Incidentul Iisus", 9, 1, false, false, true) + `,` + // dropped: tagged foreign edition (despite 9 readers)
			book(13, "The Egg and Other Stories", 134, 1, true, true, false) + `,` + // dropped: compilation
			book(14, "Nebula Winners Fifteen", 400, 9, false, true, false) + `,` + // dropped: anthology (9 authors)
			book(15, "Digitocracy", 19, 1, false, false, false) + // kept: no tagged language, but 19 readers
			`]}]}}`,
	}
	c := mockAPI(t, resp, WithLanguage("english"))
	a, err := c.GetAuthor(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, b := range a.Books {
		titles = append(titles, b.Title)
	}
	want := []string{"The Martian", "Digitocracy"}
	if len(titles) != len(want) || titles[0] != want[0] || titles[1] != want[1] {
		t.Fatalf("de-junked bibliography = %v, want %v", titles, want)
	}

	// With no language preference, the language filter is off — but compilations
	// (default-hidden) and anthologies still go.
	none := mockAPI(t, resp)
	an, _ := none.GetAuthor(context.Background(), "1")
	if len(an.Books) != 4 { // The Martian, Марсианин, Incidentul Iisus, Digitocracy
		t.Fatalf("no-language-pref: got %d books, want 4", len(an.Books))
	}
}

func TestGetAuthor(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Author": `{"data": {"authors": [{
			"id": 123,
			"name": "Terry Pratchett",
			"bio": "Sir Terry.",
			"cached_image": {"url": "https://img.example/tp.jpg"},
			"contributions": [
				{"book": {"id": 1, "title": "The Colour of Magic", "release_date": "1983-11-24",
					"rating": 4.1, "cached_image": {"url": "https://img.example/tcom.jpg"},
					"book_series": [{"position": 1, "series": {"id": 7, "name": "Discworld", "description": "The Disc."}}]}},
				{"book": {"id": 1, "title": "The Colour of Magic (dupe contribution)"}},
				{"book": null}
			]
		}]}}`,
	})

	a, err := c.GetAuthor(context.Background(), "123")
	if err != nil {
		t.Fatalf("GetAuthor: %v", err)
	}
	if a.Name != "Terry Pratchett" || a.Description != "Sir Terry." {
		t.Errorf("author = %+v", a)
	}
	if len(a.Books) != 1 {
		t.Fatalf("got %d books, want 1 (dupes and null books skipped)", len(a.Books))
	}
	b := a.Books[0]
	if b.ForeignID != "1" || b.AuthorForeignID != "123" || b.AuthorName != "Terry Pratchett" {
		t.Errorf("book = %+v", b)
	}
	if len(b.Series) != 1 || b.Series[0].Title != "Discworld" || b.Series[0].Position != 1 {
		t.Errorf("series = %+v", b.Series)
	}
}

func TestGetAuthorNotFound(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Author": `{"data": {"authors": []}}`,
	})
	if _, err := c.GetAuthor(context.Background(), "999"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	// Non-numeric ids can't exist at Hardcover.
	if _, err := c.GetAuthor(context.Background(), "abc"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("non-numeric id: err = %v, want ErrNotFound", err)
	}
}

func TestGetBook(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Book": `{"data": {"books": [{
			"id": 1,
			"title": "The Colour of Magic",
			"description": "The Discworld begins.",
			"release_date": "1983-11-24",
			"rating": 4.1,
			"cached_image": {"url": "https://img.example/tcom.jpg"},
			"contributions": [{"author": {"id": 123, "name": "Terry Pratchett"}}],
			"book_series": [{"position": 1, "series": {"id": 7, "name": "Discworld"}}],
			"editions": [
				{"id": 11, "title": "The Colour of Magic", "isbn_13": "9780061020711", "asin": "",
				 "release_date": "2009-10-13", "reading_format_id": 4,
				 "cached_image": {"url": "https://img.example/ed11.jpg"},
				 "publisher": {"name": "Harper"}, "language": {"language": "English"}},
				{"id": 12, "isbn_13": "", "asin": "B000W94ATC", "reading_format_id": 2,
				 "publisher": null, "language": null},
				{"id": 13, "reading_format_id": 1}
			]
		}]}}`,
	})

	b, err := c.GetBook(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if b.Title != "The Colour of Magic" || b.AuthorForeignID != "123" {
		t.Errorf("book = %+v", b)
	}
	if len(b.Editions) != 3 {
		t.Fatalf("got %d editions, want 3", len(b.Editions))
	}
	ebook := b.Editions[0]
	if ebook.Format != "ebook" || ebook.ISBN13 != "9780061020711" || ebook.Publisher != "Harper" || ebook.Language != "English" {
		t.Errorf("ebook edition = %+v", ebook)
	}
	if b.Editions[1].Format != "audiobook" || b.Editions[1].ASIN != "B000W94ATC" {
		t.Errorf("audio edition = %+v", b.Editions[1])
	}
	if b.Editions[2].Format != "physical" {
		t.Errorf("physical edition = %+v", b.Editions[2])
	}
	if len(b.Series) != 1 || b.Series[0].ForeignID != "7" {
		t.Errorf("series = %+v", b.Series)
	}
}

// TestValidateUnreachableVsRejected: a connection that never reaches
// Hardcover reports metadata.ErrUnreachable (so the health banner can say
// "unreachable" rather than "your token is wrong"); a 401 response does not.
func TestValidateUnreachableVsRejected(t *testing.T) {
	// No server listening at this port — the request fails at the transport
	// level, before any HTTP response.
	down := New("test-token", WithEndpoint("http://127.0.0.1:1"))
	if err := down.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("transport failure: err = %v, want ErrUnreachable", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"invalid token"}`))
	}))
	t.Cleanup(srv.Close)
	rejected := New("bad-token", WithEndpoint(srv.URL))
	if err := rejected.Validate(context.Background()); err == nil || errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("401 response: err = %v, want a non-ErrUnreachable error", err)
	}

	srv5xx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv5xx.Close)
	gatewayDown := New("test-token", WithEndpoint(srv5xx.URL))
	if err := gatewayDown.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("502 response: err = %v, want ErrUnreachable (server-side outage, not a bad token)", err)
	}
}

func TestGraphQLErrorSurfaces(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Book": `{"errors": [{"message": "field 'bogus' not found in type: 'books'"}]}`,
	})
	_, err := c.GetBook(context.Background(), "1")
	if err == nil {
		t.Fatal("expected error from GraphQL errors array")
	}
}
