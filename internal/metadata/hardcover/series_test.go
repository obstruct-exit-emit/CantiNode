package hardcover

import (
	"context"
	"testing"
)

func TestSeriesSearchAndGet(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Search": `{"data":{"search":{"results":{"hits":[
			{"document":{"id":"7310","name":"Death Note","books_count":12}},
			{"document":{"id":5637,"name":"Death Note: Black Edition","books_count":6}}
		]}}}}`,
		"Series": `{"data":{"series":[{
			"id":7310,"name":"Death Note","description":"Light finds a notebook.","books_count":3,
			"author":{"name":"Tsugumi Ohba"},
			"book_series":[
				{"position":1,"book":{"id":100,"title":"Death Note, Vol. 1","description":"Boredom","release_date":"2005-10-04","cached_image":{"url":"https://img/1.jpg"}}},
				{"position":2,"book":{"id":101,"title":"Death Note, Vol. 2","description":"Confluence","release_date":"2005-12-06","cached_image":{"url":"https://img/2.jpg"}}},
				{"position":3,"book":{"id":102,"title":"Death Note, Vol. 3"}}
			]
		}]}}`,
	})
	sc := &SeriesClient{Client: c, mediaType: "manga"}

	if sc.MediaType() != "manga" || sc.Name() != "hardcover" {
		t.Fatalf("MediaType/Name = %s/%s", sc.MediaType(), sc.Name())
	}

	results, err := sc.SearchSeries(context.Background(), "death note")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 2 || results[0].ForeignID != "7310" || results[0].IssueCount != 12 {
		t.Fatalf("search results = %+v", results)
	}

	s, err := sc.GetSeries(context.Background(), "7310")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if s.Title != "Death Note" || s.AuthorName != "Tsugumi Ohba" || s.IssueCount != 3 {
		t.Fatalf("series = %+v", s)
	}
	if s.CoverURL != "https://img/1.jpg" {
		t.Errorf("cover = %q, want the first volume's image", s.CoverURL)
	}
	if len(s.Issues) != 3 {
		t.Fatalf("issues = %d, want 3", len(s.Issues))
	}
	// Clean positions are used as volume numbers.
	for i, want := range []float64{1, 2, 3} {
		if s.Issues[i].Number != want {
			t.Errorf("issue %d number = %v, want %v", i, s.Issues[i].Number, want)
		}
	}
	if s.Issues[0].ForeignID != "100" || s.Issues[0].Title != "Death Note, Vol. 1" {
		t.Errorf("issue 0 = %+v", s.Issues[0])
	}
	// Per-volume description and cover come through.
	if s.Issues[0].Description != "Boredom" || s.Issues[0].CoverURL != "https://img/1.jpg" {
		t.Errorf("issue 0 description/cover = %q / %q", s.Issues[0].Description, s.Issues[0].CoverURL)
	}
	if s.Issues[1].Description != "Confluence" {
		t.Errorf("issue 1 description = %q", s.Issues[1].Description)
	}
}

// Hardcover's manga series often store every book at position 0; volume
// numbers must fall back to sequential order.
func TestSeriesMessyPositionsFallBackToSequential(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Series": `{"data":{"series":[{
			"id":7310,"name":"Death Note","books_count":3,
			"book_series":[
				{"position":0,"book":{"id":100,"title":"A"}},
				{"position":0,"book":{"id":101,"title":"B"}},
				{"position":0,"book":{"id":102,"title":"C"}}
			]
		}]}}`,
	})
	sc := &SeriesClient{Client: c, mediaType: "manga"}

	s, err := sc.GetSeries(context.Background(), "7310")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	for i, want := range []float64{1, 2, 3} {
		if s.Issues[i].Number != want {
			t.Fatalf("issue %d number = %v, want sequential %v", i, s.Issues[i].Number, want)
		}
	}
}

// A real Hardcover manga series (e.g. Death Note, 7310) mixes position-0
// spin-offs with numbered volumes, and carries several editions per volume:
// reissues/box sets alongside the standard release. GetSeries must drop the
// spin-offs and, per position, keep the standard edition — even when a reissue
// has a longer (marketing) blurb.
func TestSeriesDropsSpinOffsAndPrefersStandardEdition(t *testing.T) {
	c := mockAPI(t, map[string]string{
		"Series": `{"data":{"series":[{
			"id":7310,"name":"Death Note","books_count":9,
			"book_series":[
				{"position":0,"book":{"id":900,"title":"Another Note (novel)","description":"A prequel spin-off novel."}},
				{"position":0,"book":{"id":901,"title":"How to Read 13","description":"Guidebook extras."}},
				{"position":1,"book":{"id":100,"title":"Death Note, Vol. 1","description":"Best selling series now reissued in an amazing collector's edition with deluxe hardcover binding, larger trim, and bonus material for die-hard fans everywhere."}},
				{"position":1,"book":{"id":101,"title":"Death Note, Vol. 1: Boredom","description":"Light finds the notebook and tests it.","cached_image":{"url":"https://img/en1.jpg"}}},
				{"position":1,"book":{"id":102,"title":"DEATH NOTE 完全版 1","description":""}},
				{"position":2,"book":{"id":103,"title":"Death Note, Vol. 2: Confluence","description":"L closes in on Kira."}}
			]
		}]}}`,
	})
	sc := &SeriesClient{Client: c, mediaType: "manga"}

	s, err := sc.GetSeries(context.Background(), "7310")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if s.IssueCount != 2 || len(s.Issues) != 2 {
		t.Fatalf("issue count = %d, want 2 (spin-offs at position 0 dropped)", len(s.Issues))
	}
	// Volume 1 must be the standard edition (id 101), NOT the longer-blurbed
	// reissue (id 100) nor the description-less Japanese printing (id 102).
	if s.Issues[0].Number != 1 || s.Issues[0].ForeignID != "101" {
		t.Fatalf("volume 1 = %+v, want the standard edition (id 101), not the reissue", s.Issues[0])
	}
	if s.Issues[0].Description == "" || s.Issues[0].CoverURL != "https://img/en1.jpg" {
		t.Fatalf("volume 1 lost its description/cover: %+v", s.Issues[0])
	}
	if s.Issues[1].Number != 2 || s.Issues[1].ForeignID != "103" {
		t.Fatalf("volume 2 = %+v, want position-2 edition (id 103)", s.Issues[1])
	}
}

// TestSeriesLanguageAndCountryPreference: the global metadata preferences
// outrank every other tier — a Spanish preference picks the Spanish printing
// over the standard English one with the richer description; with two
// same-language candidates, the country preference breaks the tie. No match
// at all falls back to the usual standard/description tiers.
func TestSeriesLanguageAndCountryPreference(t *testing.T) {
	mock := map[string]string{
		"Series": `{"data":{"series":[{
			"id":7310,"name":"Death Note","books_count":4,
			"book_series":[
				{"position":9,"book":{"id":100,"title":"Death Note, Vol. 9","description":"A long, rich English synopsis of volume nine.",
					"editions":[{"language":{"language":"English"},"country":{"name":"United States of America"}}]}},
				{"position":9,"book":{"id":101,"title":"Death Note 09: Contacto","description":"Sinopsis corta.",
					"editions":[{"language":{"language":"Spanish; Castilian"},"country":{"name":"Spain"}}]}},
				{"position":10,"book":{"id":102,"title":"Death Note, Vol. 10","description":"US printing, longer description here.",
					"editions":[{"language":{"language":"English"},"country":{"name":"United States of America"}}]}},
				{"position":10,"book":{"id":103,"title":"Death Note, Vol. 10 (UK)","description":"UK printing.",
					"editions":[{"language":{"language":"English"},"country":{"name":"United Kingdom"}}]}}
			]
		}]}}`,
	}

	// Spanish preference: the Spanish edition wins volume 9 despite the
	// English one's longer description; volume 10 (no Spanish at all) falls
	// back to the description tiers.
	sc := &SeriesClient{Client: mockAPI(t, mock), mediaType: "manga", language: "spanish"}
	s, err := sc.GetSeries(context.Background(), "7310")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if s.Issues[0].ForeignID != "101" {
		t.Fatalf("vol 9 with Spanish pref = %+v, want the Spanish edition (101)", s.Issues[0])
	}
	if s.Issues[1].ForeignID != "102" {
		t.Fatalf("vol 10 with Spanish pref = %+v, want fallback to richest description (102)", s.Issues[1])
	}

	// English + United Kingdom: language ties volume 10's two candidates, the
	// country preference picks the UK printing over the longer-described US one.
	sc = &SeriesClient{Client: mockAPI(t, mock), mediaType: "manga", language: "english", country: "united kingdom"}
	if s, err = sc.GetSeries(context.Background(), "7310"); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if s.Issues[0].ForeignID != "100" {
		t.Fatalf("vol 9 with English pref = %+v, want the English edition (100)", s.Issues[0])
	}
	if s.Issues[1].ForeignID != "103" {
		t.Fatalf("vol 10 with UK pref = %+v, want the UK printing (103)", s.Issues[1])
	}
}
