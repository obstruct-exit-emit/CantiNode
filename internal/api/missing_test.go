package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

	// Same from the audiobook side.
	a.want(a.call("GET", fmt.Sprintf("/api/v1/author/%d/missing?library=audiobook", author.ID), nil, &missing), http.StatusOK)
	if len(missing) != 2 {
		t.Fatalf("audiobook missing = %d, want 2", len(missing))
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

// TestAddIsolation: adding an author or book into ONE format library must
// never enroll the other — the libraries are linked only by ownership.
func TestAddIsolation(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	var author library.Author
	a.want(a.call("POST", "/api/v1/author",
		map[string]string{"foreignAuthorId": "100", "library": "ebook"}, &author), http.StatusCreated)
	if !author.InEbookLibrary || author.InAudiobookLibrary {
		t.Fatalf("ebook author add: memberships = ebook %v audio %v, want true/false",
			author.InEbookLibrary, author.InAudiobookLibrary)
	}
	var audioAuthors []library.Author
	a.want(a.call("GET", "/api/v1/author?library=audiobook", nil, &audioAuthors), http.StatusOK)
	if len(audioAuthors) != 0 {
		t.Fatalf("audiobook library lists %d author(s) after an ebook-only add", len(audioAuthors))
	}

	// Adding one BOOK as an ebook: the book and its author stay out of
	// Audiobooks.
	var books []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &books), http.StatusOK)
	var added library.Book
	a.want(a.call("POST", "/api/v1/book",
		map[string]string{"foreignBookId": books[0].ForeignID, "library": "ebook"}, &added), http.StatusCreated)
	if !added.InEbookLibrary || added.InAudiobookLibrary || added.AudiobookMonitored {
		t.Fatalf("ebook book add: flags ebook %v / audio %v / audioMon %v, want ebook-only",
			added.InEbookLibrary, added.InAudiobookLibrary, added.AudiobookMonitored)
	}
	a.want(a.call("GET", "/api/v1/author?library=audiobook", nil, &audioAuthors), http.StatusOK)
	if len(audioAuthors) != 0 {
		t.Fatalf("audiobook library gained an author from an ebook book-add")
	}
}

// TestScanCrossFormatGuard: a scan must not silently attach a file to a book
// that belongs only to the OTHER format library — the exact "I added the
// ebook and it showed up in Audiobooks" linkage. The file stays unmatched
// with a confident suggestion; the one-click import is the consent step.
func TestScanCrossFormatGuard(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// An ebook-library-only book…
	var author library.Author
	a.want(a.call("POST", "/api/v1/author",
		map[string]string{"foreignAuthorId": "100", "library": "ebook"}, &author), http.StatusCreated)
	var books []library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book?authorId=%d", author.ID), nil, &books), http.StatusOK)
	var added library.Book
	a.want(a.call("POST", "/api/v1/book",
		map[string]string{"foreignBookId": books[0].ForeignID, "library": "ebook"}, &added), http.StatusCreated)

	// …and a matching AUDIOBOOK file on disk.
	audioRoot := t.TempDir()
	dir := filepath.Join(audioRoot, "Terry Pratchett")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "The Colour of Magic.m4b"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "audiobook", "path": audioRoot}, nil), http.StatusCreated)
	a.want(a.call("POST", "/api/v1/library/scan", nil, nil), http.StatusOK)

	// The book must NOT be in the audiobook library, and the file must be an
	// unmatched stray…
	var after library.Book
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", added.ID), nil, &after), http.StatusOK)
	if after.InAudiobookLibrary || after.HasAudiobookFile {
		t.Fatalf("scan silently attached a cross-format file: inAudio %v hasAudioFile %v",
			after.InAudiobookLibrary, after.HasAudiobookFile)
	}
	var options []unmatchedOption
	a.want(a.call("GET", "/api/v1/bookfile/unmatched/options?mediaType=audiobook", nil, &options), http.StatusOK)
	if len(options) != 1 || !options[0].Confident || options[0].Suggested != added.ID {
		t.Fatalf("stray options = %+v, want one confident suggestion for the book", options)
	}

	// …whose one-click import IS the consent that enrolls the audiobook side.
	a.want(a.call("POST", fmt.Sprintf("/api/v1/bookfile/%d/match", options[0].File.ID),
		map[string]int64{"bookId": added.ID}, nil), http.StatusOK)
	a.want(a.call("GET", fmt.Sprintf("/api/v1/book/%d", added.ID), nil, &after), http.StatusOK)
	if !after.InAudiobookLibrary || !after.HasAudiobookFile {
		t.Fatalf("import should enroll + attach: inAudio %v hasAudioFile %v",
			after.InAudiobookLibrary, after.HasAudiobookFile)
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
	if after.InAudiobookLibrary {
		t.Fatal("after refresh: book gained audiobook membership")
	}
}

// TestListBooksScopedByLibrary: GET /book?library= filters server-side to
// that format's member books — the Ebooks/Audiobooks page's manual-match
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
	// Monitor one book into ebook, the other into audiobook.
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", all[0].ID),
		map[string]any{"library": "ebook", "member": true, "monitored": true}, nil), http.StatusOK)
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/book/%d/library", all[1].ID),
		map[string]any{"library": "audiobook", "member": true, "monitored": true}, nil), http.StatusOK)

	var ebooks, audiobooks []library.Book
	a.want(a.call("GET", "/api/v1/book?library=ebook", nil, &ebooks), http.StatusOK)
	if len(ebooks) != 1 || ebooks[0].ID != all[0].ID {
		t.Fatalf("ebook-scoped = %+v, want just %+v", ebooks, all[0])
	}
	a.want(a.call("GET", "/api/v1/book?library=audiobook", nil, &audiobooks), http.StatusOK)
	if len(audiobooks) != 1 || audiobooks[0].ID != all[1].ID {
		t.Fatalf("audiobook-scoped = %+v, want just %+v", audiobooks, all[1])
	}

	// An invalid library value is a 400, not a 500.
	a.want(a.call("GET", "/api/v1/book?library=bogus", nil, nil), http.StatusBadRequest)
}

// TestLibraryRefresh: the library-wide metadata refresh counts the library's
// records, refuses provider-less magazines, and answers 202 for a real run.
func TestLibraryRefresh(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	a.want(a.call("POST", "/api/v1/library/refresh",
		map[string]string{"mediaType": "magazine"}, nil), http.StatusBadRequest)

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
