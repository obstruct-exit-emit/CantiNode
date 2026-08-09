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

// TestAuthorLibraryMembershipIndependence covers the fix for cross-format
// bleed: an author's ebook and audiobook membership must be independently
// settable, and adding a book to one format must not touch the other.
func TestAuthorLibraryMembershipIndependence(t *testing.T) {
	s := newTestStore(t)

	a := &Author{Source: "t", ForeignID: "a1", Name: "Terry"}
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatal(err)
	}
	b := &Book{AuthorID: a.ID, Source: "t", ForeignID: "b1", Title: "Mort"}
	if err := s.UpsertBook(b); err != nil {
		t.Fatal(err)
	}

	// A book joining the ebook library enrolls the author in ebooks only.
	if err := s.SetBookLibrary(b.ID, "ebook", true, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAuthor(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.InEbookLibrary || got.InAudiobookLibrary {
		t.Fatalf("author membership after ebook add = %+v, want ebook only", got)
	}

	// Cross-adding to audiobook enrolls the author there too, without
	// touching ebook membership.
	if err := s.SetBookLibrary(b.ID, "audiobook", true, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAuthor(a.ID)
	if !got.InEbookLibrary || !got.InAudiobookLibrary {
		t.Fatalf("author membership after audiobook cross-add = %+v, want both", got)
	}

	// Removing the author from audiobooks (RemoveAuthorBooksLibrary + the
	// author flag) must leave ebook membership and the book's ebook
	// membership completely untouched.
	if err := s.SetAuthorLibrary(a.ID, "audiobook", false); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAuthorBooksLibrary(a.ID, "audiobook"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAuthor(a.ID)
	if !got.InEbookLibrary || got.InAudiobookLibrary {
		t.Fatalf("author membership after audiobook removal = %+v, want ebook only", got)
	}
	book, err := s.GetBook(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !book.InEbookLibrary || !book.EbookMonitored || book.InAudiobookLibrary || book.AudiobookMonitored {
		t.Fatalf("book membership after author audiobook removal = %+v, want ebook untouched, audiobook cleared", book)
	}
}
