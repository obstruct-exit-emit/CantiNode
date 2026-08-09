package library

import (
	"testing"
	"time"
)

// TestCalendarNavIDs: calendar items carry the ids the UI navigates with —
// a prose book its author id, a volume/issue its series id.
func TestCalendarNavIDs(t *testing.T) {
	s := newTestStore(t)
	today := time.Now().UTC().Format("2006-01-02")

	// Prose: an ebook-library book dated today.
	a := testAuthor()
	if err := s.UpsertAuthor(a); err != nil {
		t.Fatalf("UpsertAuthor: %v", err)
	}
	b := &Book{
		AuthorID: a.ID, Source: "hardcover", ForeignID: "cal-book",
		MediaType: "book", Title: "Calendar Book", ReleaseDate: today,
	}
	if err := s.UpsertBook(b); err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	if err := s.SetBookLibrary(b.ID, "ebook", true, true); err != nil {
		t.Fatalf("SetBookLibrary: %v", err)
	}

	// Magazine: an issue dated today (CreateMagazineIssue links series_books).
	sr := &Series{Source: "manual", ForeignID: "mag-cal", MediaType: "magazine", Title: "Calendar Weekly"}
	if err := s.UpsertSeries(sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if _, err := s.CreateMagazineIssue(sr, today, false); err != nil {
		t.Fatalf("CreateMagazineIssue: %v", err)
	}

	items, err := s.Calendar(today, today)
	if err != nil {
		t.Fatalf("Calendar: %v", err)
	}
	byType := map[string]CalendarItem{}
	for _, it := range items {
		byType[it.MediaType] = it
	}
	eb, ok := byType["ebook"]
	if !ok || eb.AuthorID != a.ID {
		t.Errorf("ebook item = %+v, ok=%v; want authorId %d", eb, ok, a.ID)
	}
	mg, ok := byType["magazine"]
	if !ok || mg.SeriesID != sr.ID {
		t.Errorf("magazine item = %+v, ok=%v; want seriesId %d", mg, ok, sr.ID)
	}
}
