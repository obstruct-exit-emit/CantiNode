package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/librinode/librinode/internal/library"
)

// TestAuthorMissing: books neither monitored nor owned in a format library
// surface as the author's bibliography gaps, with series info for grouping.
func TestAuthorMissing(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)

	// Adding an author enrolls NO books — the whole bibliography starts as
	// Missing, for the user to monitor selectively.
	var missing []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d/missing?library=ebook", author.ID), nil, &missing), http.StatusOK)
	if len(missing) != 2 {
		t.Fatalf("fresh author has %d missing, want the whole bibliography (2)", len(missing))
	}
	// Series books sort before standalones, and carry their series link.
	if missing[0].Title != "The Colour of Magic" || len(missing[0].Series) != 1 || missing[0].Series[0].Title != "Discworld" {
		t.Errorf("first missing = %+v, want The Colour of Magic with Discworld link", missing[0])
	}
	if missing[1].Title != "Mort" || len(missing[1].Series) != 0 {
		t.Errorf("second missing = %+v, want standalone Mort", missing[1])
	}

	// Monitoring a book (the one-click Monitor button) clears its gap.
	var books []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &books), http.StatusOK)
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", books[0].ID),
		map[string]any{"library": "ebook", "member": true, "monitored": true}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d/missing?library=ebook", author.ID), nil, &missing), http.StatusOK)
	if len(missing) != 1 || missing[0].ID == books[0].ID {
		t.Fatalf("after monitor, ebook missing = %+v, want only the other book", missing)
	}

	// Unmonitoring keeps the book in the library — membership decides
	// visibility, not the monitored flag — so it stays OUT of Missing.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", books[0].ID),
		map[string]any{"library": "ebook", "member": true, "monitored": false}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d/missing?library=ebook", author.ID), nil, &missing), http.StatusOK)
	if len(missing) != 1 {
		t.Fatalf("after unmonitor (still a member), missing = %d, want 1", len(missing))
	}

	// Removing membership is what returns a book to Missing.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", books[0].ID),
		map[string]any{"library": "ebook", "member": false}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d/missing?library=ebook", author.ID), nil, &missing), http.StatusOK)
	if len(missing) != 2 {
		t.Fatalf("after removing membership, missing = %d, want 2", len(missing))
	}
}

// TestRefreshPreservesMembership: metadata refresh must never enroll,
// un-enroll, or re-monitor — a deliberately added ebook stays an ebook
// library member across an author refresh.
func TestRefreshPreservesMembership(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author",
		map[string]string{"foreignAuthorId": "100", "library": "ebook"}, &author), http.StatusCreated)
	var books []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &books), http.StatusOK)
	var added library.Book
	a.want(a.call("POST", "/api/v1/book",
		map[string]string{"foreignBookId": books[0].ForeignID, "library": "ebook"}, &added), http.StatusCreated)

	a.want(a.call("POST", fmt.Sprintf("/api/v1/author/%d/refresh", author.ID), nil, nil), http.StatusOK)

	var after library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", added.ID), nil, &after), http.StatusOK)
	if !after.InEbookLibrary || !after.EbookMonitored {
		t.Fatalf("after refresh: inEbook %v ebookMonitored %v — refresh un-enrolled the book",
			after.InEbookLibrary, after.EbookMonitored)
	}
}

// TestListBooksScopedByLibrary: GET /book?library= filters server-side to
// the ebook library's member books — the Ebooks page's manual-match
// fallback list shouldn't have to ship every book of every media type (and
// every author's whole database) just to populate it.
func TestListBooksScopedByLibrary(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	var all []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &all), http.StatusOK)
	if len(all) != 2 {
		t.Fatalf("fixture author has %d books, want 2", len(all))
	}
	// Monitor only one book into the ebook library; the other stays a
	// bibliography stub.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", all[0].ID),
		map[string]any{"library": "ebook", "member": true, "monitored": true}, nil), http.StatusOK)

	var ebooks []library.Book
	a.want(a.call("GET", "/api/v1/book?library=ebook", nil, &ebooks), http.StatusOK)
	if len(ebooks) != 1 || ebooks[0].ID != all[0].ID {
		t.Fatalf("ebook-scoped = %+v, want just %+v", ebooks, all[0])
	}

	// An invalid library value is a 400, not a 500.
	a.want(a.call("GET", "/api/v1/book?library=bogus", nil, nil), http.StatusBadRequest)
}

// TestLibraryRefresh: the library-wide metadata refresh counts the library's
// records, refuses an unknown media type, and answers 202 for a real run.
func TestLibraryRefresh(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	a.want(a.call("POST", "/api/v1/library/refresh",
		map[string]string{"mediaType": "bogus"}, nil), http.StatusBadRequest)

	var res struct {
		Started int    `json:"started"`
		Message string `json:"message"`
	}
	a.want(a.call("POST", "/api/v1/library/refresh",
		map[string]string{"mediaType": "ebook"}, &res), http.StatusOK)
	if res.Started != 0 {
		t.Fatalf("empty library started = %d, want 0", res.Started)
	}

	var author library.Author
	a.want(a.call("POST", "/api/v1/author", map[string]string{"foreignAuthorId": "100"}, &author), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/refresh",
		map[string]string{"mediaType": "ebook"}, &res), http.StatusAccepted)
	if res.Started != 1 || res.Message == "" {
		t.Fatalf("refresh response = %+v, want started 1 with a message", res)
	}
}
