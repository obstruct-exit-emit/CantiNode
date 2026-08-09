package openlibrary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/metadata"
)

func mockOL(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, body := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return New(WithBaseURL(srv.URL))
}

func TestSearchBooksStampsSourceAndKey(t *testing.T) {
	c := mockOL(t, map[string]string{
		"/search.json": `{"docs":[
			{"key":"/works/OL45804W","title":"Fantastic Mr Fox","author_name":["Roald Dahl"],
			 "author_key":["OL34184A"],"first_publish_year":1970,"cover_i":12345,"ratings_average":4.2}
		]}`,
	})
	books, err := c.SearchBooks(context.Background(), "fox")
	if err != nil {
		t.Fatalf("SearchBooks: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("got %d books, want 1", len(books))
	}
	b := books[0]
	if b.ForeignID != "OL45804W" {
		t.Errorf("ForeignID = %q, want OL45804W (path stripped)", b.ForeignID)
	}
	if b.Source != "openlibrary" {
		t.Errorf("Source = %q, want openlibrary", b.Source)
	}
	if b.AuthorName != "Roald Dahl" || b.AuthorForeignID != "OL34184A" {
		t.Errorf("author = %q/%q", b.AuthorName, b.AuthorForeignID)
	}
	if b.ReleaseDate != "1970" {
		t.Errorf("ReleaseDate = %q, want 1970", b.ReleaseDate)
	}
	if !strings.Contains(b.CoverURL, "12345") {
		t.Errorf("CoverURL = %q, want it to reference cover 12345", b.CoverURL)
	}
}

func TestGetBookMergesWorkAuthorAndEditions(t *testing.T) {
	c := mockOL(t, map[string]string{
		"/works/OL45804W/editions.json": `{"entries":[
			{"key":"/books/OL1M","title":"Fantastic Mr Fox","isbn_13":["9780140328721"],
			 "publishers":["Puffin"],"publish_date":"1988","languages":[{"key":"/languages/eng"}]}
		]}`,
		"/works/OL45804W.json": `{"key":"/works/OL45804W","title":"Fantastic Mr Fox",
			"description":{"value":"A fox outwits three farmers."},"covers":[12345],
			"authors":[{"author":{"key":"/authors/OL34184A"}}]}`,
		"/authors/OL34184A.json": `{"key":"/authors/OL34184A","name":"Roald Dahl","bio":"British author."}`,
	})
	b, err := c.GetBook(context.Background(), "OL45804W")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if b.Description != "A fox outwits three farmers." {
		t.Errorf("Description = %q (object-form value not unwrapped?)", b.Description)
	}
	if b.AuthorForeignID != "OL34184A" || b.AuthorName != "Roald Dahl" {
		t.Errorf("author = %q/%q, want OL34184A/Roald Dahl", b.AuthorForeignID, b.AuthorName)
	}
	if len(b.Editions) != 1 || b.Editions[0].ISBN13 != "9780140328721" {
		t.Fatalf("editions = %+v, want one with ISBN 9780140328721", b.Editions)
	}
	if b.Editions[0].Publisher != "Puffin" || b.Editions[0].Language != "eng" {
		t.Errorf("edition publisher/lang = %q/%q", b.Editions[0].Publisher, b.Editions[0].Language)
	}
	if b.Source != "openlibrary" {
		t.Errorf("Source = %q", b.Source)
	}
}

func TestGetBookNotFound(t *testing.T) {
	c := mockOL(t, map[string]string{}) // everything 404s
	_, err := c.GetBook(context.Background(), "OLmissingW")
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestValidateUnreachable(t *testing.T) {
	down := New(WithBaseURL("http://127.0.0.1:1"))
	if err := down.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}
