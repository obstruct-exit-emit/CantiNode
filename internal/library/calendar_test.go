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

	// Comic: a volume dated today, linked to its series.
	sr := &Series{Source: "comicvine", ForeignID: "comic-cal", MediaType: "comic", Title: "Calendar Comic"}
	if err := s.UpsertSeries(sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	cb := &Book{
		AuthorID: a.ID, Source: "comicvine", ForeignID: "comic-cal-1",
		MediaType: "comic", Title: "Calendar Comic #1", ReleaseDate: today,
	}
	if err := s.UpsertBook(cb); err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	if err := s.LinkBookSeries(cb.ID, sr.ID, 1); err != nil {
		t.Fatalf("LinkBookSeries: %v", err)
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
	cm, ok := byType["comic"]
	if !ok || cm.SeriesID != sr.ID {
		t.Errorf("comic item = %+v, ok=%v; want seriesId %d", cm, ok, sr.ID)
	}
}
