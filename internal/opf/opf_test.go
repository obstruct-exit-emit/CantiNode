package opf

import (
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/library"
)

func TestRender(t *testing.T) {
	book := &library.Book{
		Title:       "Mort",
		Description: "Death takes an apprentice & things go wrong.",
		ReleaseDate: "1987-11-12",
		Editions: []library.Edition{
			{Format: "ebook", ISBN13: "9780552131063", Language: "english"},
		},
	}
	out, err := Render(book, "Terry Pratchett", []library.SeriesLink{{Title: "Discworld", Position: 4}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<dc:title>Mort</dc:title>`,
		`opf:role="aut"`,
		`Terry Pratchett`,
		`&amp; things go wrong`, // escaping
		`opf:scheme="ISBN"`,
		`9780552131063`,
		`<dc:language>english</dc:language>`,
		`name="calibre:series" content="Discworld"`,
		`name="calibre:series_index" content="4"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}

	// Sparse books render without optional elements.
	sparse, err := Render(&library.Book{Title: "Untitled"}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sparse), "dc:creator") || strings.Contains(string(sparse), "calibre:series") {
		t.Errorf("sparse render has optional elements:\n%s", sparse)
	}
}
