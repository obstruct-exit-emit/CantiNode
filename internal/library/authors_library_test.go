package library

import "testing"

// TestListAuthorsInLibraryCountsAfterRemoval is a regression test: the
// Author.BookCount JSON field used to have `omitempty`, which silently
// dropped a legitimate zero value from API responses and made an author
// with no visible books look indistinguishable from a decode failure.
func TestListAuthorsInLibraryCountsAfterRemoval(t *testing.T) {
	s := newTestStore(t)

	a := &Author{Source: "t", ForeignID: "a1", Name: "Terry"}
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatal(err)
	}
	b := &Book{AuthorID: a.ID, Source: "t", ForeignID: "b1", Title: "Mort"}
	if err := s.UpsertBook(b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBookLibrary(b.ID, "ebook", true, true); err != nil {
		t.Fatal(err)
	}

	// Removing the book's ebook membership must leave the author listed
	// (author-level membership persists) with zero visible books.
	if err := s.SetBookLibrary(b.ID, "ebook", false, false); err != nil {
		t.Fatal(err)
	}

	authors, err := s.ListAuthorsInLibrary("ebook")
	if err != nil {
		t.Fatal(err)
	}
	if len(authors) != 1 {
		t.Fatalf("authors = %+v, want 1 (author membership persists)", authors)
	}
	if authors[0].BookCount != 0 || authors[0].OwnedCount != 0 {
		t.Fatalf("counts = %d/%d, want 0/0", authors[0].OwnedCount, authors[0].BookCount)
	}
}
