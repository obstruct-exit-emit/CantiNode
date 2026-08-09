package library

import "testing"

// TestWantedCarriesSeriesAndMeta guards the homeItems query behind Wanted(): a
// monitored, unowned book comes back with its series title + position and its
// release date + rating, so the UI can sort by them. It exercises the series
// subquery — whose column names have silently drifted before — end to end.
func TestWantedCarriesSeriesAndMeta(t *testing.T) {
	s := newTestStore(t)
	a := testAuthor()
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatalf("UpsertAuthor: %v", err)
	}
	b := &Book{
		AuthorID: a.ID, Source: "hardcover", ForeignID: "b1",
		Title: "Dune Messiah", ReleaseDate: "1969-10-15", Rating: 3.8, Monitored: true,
	}
	if err := s.UpsertBook(b); err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	sr := &Series{Source: "hardcover", ForeignID: "s1", Title: "Dune"}
	if err := s.UpsertSeries(sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := s.LinkBookSeries(b.ID, sr.ID, 2); err != nil {
		t.Fatalf("LinkBookSeries: %v", err)
	}
	// In the ebook library, monitored, no file → wanted.
	if err := s.SetBookLibrary(b.ID, "ebook", true, true); err != nil {
		t.Fatalf("SetBookLibrary: %v", err)
	}

	items, err := s.Wanted("ebook")
	if err != nil {
		t.Fatalf("Wanted: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d wanted items, want 1", len(items))
	}
	it := items[0]
	if it.SeriesTitle != "Dune" || it.SeriesPosition != 2 {
		t.Errorf("series = %q #%v, want Dune #2", it.SeriesTitle, it.SeriesPosition)
	}
	if it.ReleaseDate != "1969-10-15" || it.Rating != 3.8 {
		t.Errorf("date/rating = %q/%v, want 1969-10-15/3.8", it.ReleaseDate, it.Rating)
	}
}
