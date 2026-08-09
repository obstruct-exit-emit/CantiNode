package googlebooks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/metadata"
)

const volumeJSON = `{
	"id":"zyTCAlFPjgYC",
	"volumeInfo":{
		"title":"The Google Story","subtitle":"Inside the Hottest Business",
		"authors":["David A. Vise","Mark Malseed"],
		"description":"How Google works.","publishedDate":"2005-11-15","publisher":"Delacorte",
		"language":"en","averageRating":3.9,
		"imageLinks":{"thumbnail":"http://books.google.com/books/content?id=zyTCAlFPjgYC"},
		"industryIdentifiers":[{"type":"ISBN_13","identifier":"9780553804577"}]
	}
}`

func mockGB(t *testing.T, search, volume string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/volumes/") {
			if volume == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(volume))
			return
		}
		w.Write([]byte(search))
	}))
	t.Cleanup(srv.Close)
	return New("", WithBaseURL(srv.URL))
}

func TestSearchBooksStampsSourceAndUpgradesCover(t *testing.T) {
	c := mockGB(t, `{"items":[`+volumeJSON+`]}`, "")
	books, err := c.SearchBooks(context.Background(), "google story")
	if err != nil {
		t.Fatalf("SearchBooks: %v", err)
	}
	if len(books) != 1 {
		t.Fatalf("got %d, want 1", len(books))
	}
	b := books[0]
	if b.ForeignID != "zyTCAlFPjgYC" {
		t.Errorf("ForeignID = %q", b.ForeignID)
	}
	if b.Title != "The Google Story: Inside the Hottest Business" {
		t.Errorf("Title = %q (subtitle not joined?)", b.Title)
	}
	if b.Source != "googlebooks" {
		t.Errorf("Source = %q", b.Source)
	}
	if b.AuthorName != "David A. Vise" || b.AuthorForeignID != "author:david a. vise" {
		t.Errorf("author = %q/%q", b.AuthorName, b.AuthorForeignID)
	}
	if !strings.HasPrefix(b.CoverURL, "https://") {
		t.Errorf("CoverURL = %q, want http upgraded to https", b.CoverURL)
	}
	if len(b.Editions) != 1 || b.Editions[0].ISBN13 != "9780553804577" {
		t.Errorf("editions = %+v", b.Editions)
	}
}

func TestGetBookRejectsSynthesizedAuthorID(t *testing.T) {
	c := mockGB(t, "", volumeJSON)
	// An "author:*" id is not a volume id — it must not be sent to the volume
	// endpoint; the chain expects ErrNotFound so it moves on.
	_, err := c.GetBook(context.Background(), "author:someone")
	if !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetBookByVolumeID(t *testing.T) {
	c := mockGB(t, "", volumeJSON)
	b, err := c.GetBook(context.Background(), "zyTCAlFPjgYC")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if b.Source != "googlebooks" || b.ForeignID != "zyTCAlFPjgYC" {
		t.Errorf("book = %+v", b)
	}
}

func TestGetAuthorFromInauthorSearch(t *testing.T) {
	c := mockGB(t, `{"items":[`+volumeJSON+`]}`, "")
	a, err := c.GetAuthor(context.Background(), "author:david a. vise")
	if err != nil {
		t.Fatalf("GetAuthor: %v", err)
	}
	if a.Name != "David A. Vise" {
		t.Errorf("Name = %q, want the provider's casing", a.Name)
	}
	if len(a.Books) != 1 {
		t.Errorf("Books = %d, want 1", len(a.Books))
	}
	if a.Source != "googlebooks" {
		t.Errorf("Source = %q", a.Source)
	}
}

func TestValidateUnreachable(t *testing.T) {
	down := New("", WithBaseURL("http://127.0.0.1:1"))
	if err := down.Validate(context.Background()); !errors.Is(err, metadata.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}
