package metadata

import (
	"context"
	"errors"
	"testing"
)

// stubProvider is a scriptable Provider for exercising the fallback chain.
type stubProvider struct {
	name    string
	books   []Book
	authors []Author
	book    *Book
	author  *Author
	err     error // returned by Search* and, when non-nil, Get* (unless getErr)
	getErr  error // overrides err for Get* when set
}

func (p stubProvider) Name() string { return p.name }
func (p stubProvider) SearchAuthors(context.Context, string) ([]Author, error) {
	return p.authors, p.err
}
func (p stubProvider) SearchBooks(context.Context, string) ([]Book, error) {
	return p.books, p.err
}
func (p stubProvider) GetAuthor(context.Context, string) (*Author, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.author == nil {
		return nil, ErrNotFound
	}
	return p.author, nil
}
func (p stubProvider) GetBook(context.Context, string) (*Book, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	if p.book == nil {
		return nil, ErrNotFound
	}
	return p.book, nil
}

func TestNewFallbackCollapsesWhenNoUsableFallback(t *testing.T) {
	primary := stubProvider{name: "primary"}
	// No fallbacks, a nil one, and one that duplicates the primary all collapse.
	if got := NewFallback(primary); got.Name() != "primary" {
		t.Fatalf("no fallback: got %T", got)
	}
	if _, ok := NewFallback(primary, nil).(stubProvider); !ok {
		t.Error("nil fallback should collapse to the bare primary")
	}
	if _, ok := NewFallback(primary, stubProvider{name: "primary"}).(stubProvider); !ok {
		t.Error("a fallback duplicating the primary should collapse to the bare primary")
	}
}

func TestFallbackSearchOnlyWhenPrimaryEmpty(t *testing.T) {
	primary := stubProvider{name: "primary", books: []Book{{ForeignID: "p1", Title: "Primary Book"}}}
	fb := stubProvider{name: "fb", books: []Book{{ForeignID: "f1", Title: "Fallback Book"}}}

	// Primary has results → fallback is never consulted, no Source stamping.
	got, err := NewFallback(primary, fb).SearchBooks(context.Background(), "q")
	if err != nil || len(got) != 1 || got[0].ForeignID != "p1" || got[0].Source != "" {
		t.Fatalf("primary-hit: got %+v err %v", got, err)
	}

	// Primary empty → fallback answers, and every result is stamped with its
	// origin so the eventual add records the right source.
	empty := stubProvider{name: "primary"}
	got, err = NewFallback(empty, fb).SearchBooks(context.Background(), "q")
	if err != nil || len(got) != 1 || got[0].ForeignID != "f1" || got[0].Source != "fb" {
		t.Fatalf("fallback-hit: got %+v err %v", got, err)
	}
}

func TestFallbackGetBookRoutesAndStamps(t *testing.T) {
	// Primary doesn't have the id; the fallback does. The chain returns the
	// fallback's book, stamped with its name.
	primary := stubProvider{name: "primary"} // GetBook → ErrNotFound
	fb := stubProvider{name: "fb", book: &Book{ForeignID: "f1", Title: "Found"}}

	got, err := NewFallback(primary, fb).GetBook(context.Background(), "f1")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if got.Source != "fb" {
		t.Errorf("Source = %q, want fb", got.Source)
	}

	// Nobody has it → ErrNotFound surfaces.
	_, err = NewFallback(primary, stubProvider{name: "fb"}).GetBook(context.Background(), "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("all-miss: err = %v, want ErrNotFound", err)
	}
}

func TestFallbackGetBookNonNotFoundErrorSurfaces(t *testing.T) {
	// A real transport error from the primary must not be masked by trying a
	// fallback that never owned the id — only ErrNotFound falls through.
	boom := errors.New("boom")
	primary := stubProvider{name: "primary", getErr: boom}
	fb := stubProvider{name: "fb", book: &Book{ForeignID: "f1"}}

	_, err := NewFallback(primary, fb).GetBook(context.Background(), "f1")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the primary's transport error", err)
	}
}

func TestFallbackPreservesExplicitSource(t *testing.T) {
	// When a provider already sets Source on a record, the chain leaves it.
	fb := stubProvider{name: "fb", book: &Book{ForeignID: "f1", Source: "explicit"}}
	got, err := NewFallback(stubProvider{name: "primary"}, fb).GetBook(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "explicit" {
		t.Errorf("Source = %q, want the provider's explicit value", got.Source)
	}
}
