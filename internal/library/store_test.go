package library

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func testAuthor() *Author {
	return &Author{
		Source:      "hardcover",
		ForeignID:   "auth-1",
		Name:        "Terry Pratchett",
		Description: "Discworld's architect.",
		ImageURL:    "https://img.example/tp.jpg",
		Monitored:   true,
	}
}

func TestSortName(t *testing.T) {
	cases := map[string]string{
		"Terry Pratchett":   "Pratchett, Terry",
		"Ursula K. Le Guin": "Guin, Ursula K. Le", // naive split; provider data can refine later
		"Homer":             "Homer",
		"":                  "",
	}
	for in, want := range cases {
		if got := SortName(in); got != want {
			t.Errorf("SortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortTitle(t *testing.T) {
	cases := map[string]string{
		"The Colour of Magic": "Colour of Magic",
		"A Hat Full of Sky":   "Hat Full of Sky",
		"An Ember in Ashes":   "Ember in Ashes",
		"Mort":                "Mort",
		"The ":                "The ", // article with nothing after stays put
	}
	for in, want := range cases {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuthorCRUD(t *testing.T) {
	s := newTestStore(t)

	a := testAuthor()
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatalf("UpsertAuthor: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("expected author ID to be set")
	}
	if a.SortName != "Pratchett, Terry" {
		t.Errorf("sort name = %q, want %q", a.SortName, "Pratchett, Terry")
	}

	// Upserting the same foreign id refreshes metadata, keeps the row.
	a2 := testAuthor()
	a2.Description = "Updated bio."
	a2.Monitored = false // must NOT clobber the stored flag
	if err := s.UpsertAuthor(a2); err != nil {
		t.Fatalf("UpsertAuthor (update): %v", err)
	}
	if a2.ID != a.ID {
		t.Errorf("upsert created new row: id %d != %d", a2.ID, a.ID)
	}

	got, err := s.GetAuthor(a.ID)
	if err != nil {
		t.Fatalf("GetAuthor: %v", err)
	}
	if got.Description != "Updated bio." {
		t.Errorf("description not refreshed: %q", got.Description)
	}
	if !got.Monitored {
		t.Error("upsert clobbered monitored flag")
	}

	if _, err := s.GetAuthorByForeignID("hardcover", "auth-1"); err != nil {
		t.Errorf("GetAuthorByForeignID: %v", err)
	}

	list, err := s.ListAuthors()
	if err != nil {
		t.Fatalf("ListAuthors: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListAuthors returned %d authors, want 1", len(list))
	}

	if err := s.SetAuthorMonitored(a.ID, false); err != nil {
		t.Fatalf("SetAuthorMonitored: %v", err)
	}
	got, _ = s.GetAuthor(a.ID)
	if got.Monitored {
		t.Error("SetAuthorMonitored(false) had no effect")
	}

	if err := s.DeleteAuthor(a.ID); err != nil {
		t.Fatalf("DeleteAuthor: %v", err)
	}
	if _, err := s.GetAuthor(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetAuthor after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteAuthor(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("double delete: err = %v, want ErrNotFound", err)
	}
}

func TestBookAndEditionCRUD(t *testing.T) {
	s := newTestStore(t)

	a := testAuthor()
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatalf("UpsertAuthor: %v", err)
	}

	b := &Book{
		AuthorID:    a.ID,
		Source:      "hardcover",
		ForeignID:   "book-1",
		Title:       "The Colour of Magic",
		ReleaseDate: "1983-11-24",
		Rating:      4.1,
		Monitored:   true,
	}
	if err := s.UpsertBook(b); err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	if b.SortTitle != "Colour of Magic" {
		t.Errorf("sort title = %q", b.SortTitle)
	}

	// Refresh keeps id and monitored flag.
	b2 := *b
	b2.ID = 0
	b2.Rating = 4.3
	b2.Monitored = false
	if err := s.UpsertBook(&b2); err != nil {
		t.Fatalf("UpsertBook (update): %v", err)
	}
	if b2.ID != b.ID {
		t.Errorf("upsert created new row: id %d != %d", b2.ID, b.ID)
	}
	got, err := s.GetBook(b.ID)
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if got.Rating != 4.3 {
		t.Errorf("rating not refreshed: %v", got.Rating)
	}
	if !got.Monitored {
		t.Error("upsert clobbered monitored flag")
	}

	e := &Edition{
		BookID:    b.ID,
		Source:    "hardcover",
		ForeignID: "ed-1",
		ISBN13:    "9780061020711",
		Format:    FormatEbook,
		Monitored: true,
	}
	if err := s.UpsertEdition(e); err != nil {
		t.Fatalf("UpsertEdition: %v", err)
	}
	eds, err := s.ListEditions(b.ID)
	if err != nil {
		t.Fatalf("ListEditions: %v", err)
	}
	if len(eds) != 1 || eds[0].ISBN13 != "9780061020711" {
		t.Fatalf("unexpected editions: %+v", eds)
	}

	// Series linking.
	sr := &Series{Source: "hardcover", ForeignID: "ser-1", Title: "Discworld"}
	if err := s.UpsertSeries(sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := s.LinkBookSeries(b.ID, sr.ID, 1); err != nil {
		t.Fatalf("LinkBookSeries: %v", err)
	}
	links, err := s.ListSeriesForBook(b.ID)
	if err != nil {
		t.Fatalf("ListSeriesForBook: %v", err)
	}
	if len(links) != 1 || links[0].Title != "Discworld" || links[0].Position != 1 {
		t.Fatalf("unexpected series links: %+v", links)
	}

	// Deleting the author cascades to books, editions, series links.
	if err := s.DeleteAuthor(a.ID); err != nil {
		t.Fatalf("DeleteAuthor: %v", err)
	}
	if _, err := s.GetBook(b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("book survived author delete: err = %v", err)
	}
	if eds, _ := s.ListEditions(b.ID); len(eds) != 0 {
		t.Errorf("editions survived author delete: %+v", eds)
	}
	links, _ = s.ListSeriesForBook(b.ID)
	if len(links) != 0 {
		t.Errorf("series links survived author delete: %+v", links)
	}
}

func TestListBooksFilters(t *testing.T) {
	s := newTestStore(t)

	a1 := testAuthor()
	s.UpsertAuthor(a1)
	a2 := testAuthor()
	a2.ForeignID = "auth-2"
	a2.Name = "Neil Gaiman"
	s.UpsertAuthor(a2)

	for i, spec := range []struct {
		authorID  int64
		foreignID string
		title     string
	}{
		{a1.ID, "b1", "Mort"},
		{a1.ID, "b2", "Sourcery"},
		{a2.ID, "b3", "Coraline"},
	} {
		b := &Book{AuthorID: spec.authorID, Source: "hardcover", ForeignID: spec.foreignID, Title: spec.title}
		if err := s.UpsertBook(b); err != nil {
			t.Fatalf("UpsertBook %d: %v", i, err)
		}
	}

	all, err := s.ListBooks(0)
	if err != nil {
		t.Fatalf("ListBooks(0): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListBooks(0) returned %d books, want 3", len(all))
	}

	only1, err := s.ListBooks(a1.ID)
	if err != nil {
		t.Fatalf("ListBooks(a1): %v", err)
	}
	if len(only1) != 2 {
		t.Errorf("ListBooks(a1) returned %d books, want 2", len(only1))
	}
}
