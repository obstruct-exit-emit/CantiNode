package refresh

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

// providerState is mutableProvider's actual data, held behind a pointer so
// every copy of mutableProvider (the manager keeps its own) shares one
// mutable store — tests can reach in and change it after the provider is
// already registered.
type providerState struct {
	authors map[string]*metadata.Author
	books   map[string]*metadata.Book
	// forceUnreachable makes GetAuthor/GetBook report metadata.ErrUnreachable
	// for these foreign ids, simulating a provider outage.
	forceUnreachable map[string]bool
	// calls records every foreign id looked up via GetAuthor, in call order —
	// so a test can confirm the circuit breaker actually stopped early.
	calls []string
}

// mutableProvider serves in-memory metadata that tests can change between
// calls to simulate the provider gaining books, updating fields, or going
// unreachable.
type mutableProvider struct{ *providerState }

func (mutableProvider) Name() string { return "fake" }

func (p mutableProvider) SearchAuthors(context.Context, string) ([]metadata.Author, error) {
	return nil, nil
}

func (p mutableProvider) SearchBooks(context.Context, string) ([]metadata.Book, error) {
	return nil, nil
}

func (p mutableProvider) GetAuthor(_ context.Context, id string) (*metadata.Author, error) {
	p.calls = append(p.calls, id)
	if p.forceUnreachable[id] {
		return nil, fmt.Errorf("test provider down: %w", metadata.ErrUnreachable)
	}
	a, ok := p.authors[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	return a, nil
}

func (p mutableProvider) GetBook(_ context.Context, id string) (*metadata.Book, error) {
	if p.forceUnreachable[id] {
		return nil, fmt.Errorf("test provider down: %w", metadata.ErrUnreachable)
	}
	b, ok := p.books[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	return b, nil
}

func newFixture(t *testing.T) (*Service, *library.Store, mutableProvider) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := library.NewStore(db)
	provider := mutableProvider{&providerState{
		authors: map[string]*metadata.Author{
			"100": {
				ForeignID: "100", Name: "Terry Pratchett", Description: "Sir Terry.",
				Books: []metadata.Book{
					{ForeignID: "1", Title: "The Colour of Magic", AuthorForeignID: "100", AuthorName: "Terry Pratchett",
						Series: []metadata.SeriesLink{{ForeignID: "7", Title: "Discworld", Position: 1}}},
				},
			},
		},
		books: map[string]*metadata.Book{
			"1": {
				ForeignID: "1", Title: "The Colour of Magic", AuthorForeignID: "100", AuthorName: "Terry Pratchett",
				Editions: []metadata.Edition{
					{ForeignID: "11", ISBN13: "9780061020711", Format: "ebook"},
					{ForeignID: "12", ASIN: "B000W94ATC", Format: "audiobook"},
				},
			},
		},
		forceUnreachable: map[string]bool{},
	}}
	mgr := metadata.NewManager()
	mgr.Set(provider)
	return New(store, mgr), store, provider
}

func TestSyncWithoutProviderReturnsNotConfigured(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	svc := New(library.NewStore(db), metadata.NewManager())
	if _, err := svc.SyncAuthor(context.Background(), "100", true); !errors.Is(err, metadata.ErrNotConfigured) {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

func TestSyncAuthorThenRefreshPicksUpNewBooks(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	author, err := svc.SyncAuthor(ctx, "100", true)
	if err != nil {
		t.Fatalf("SyncAuthor: %v", err)
	}
	books, _ := store.ListBooks(author.ID)
	if len(books) != 1 {
		t.Fatalf("after sync: %d books, want 1", len(books))
	}

	// User unmonitors the book; provider later publishes a new one.
	if err := store.SetBookMonitored(books[0].ID, false); err != nil {
		t.Fatal(err)
	}
	provider.authors["100"].Books = append(provider.authors["100"].Books,
		metadata.Book{ForeignID: "2", Title: "Mort", AuthorForeignID: "100", AuthorName: "Terry Pratchett"})
	provider.authors["100"].Description = "Updated bio."

	if err := svc.RefreshAuthor(ctx, author.ID); err != nil {
		t.Fatalf("RefreshAuthor: %v", err)
	}

	got, _ := store.GetAuthor(author.ID)
	if got.Description != "Updated bio." {
		t.Errorf("description not refreshed: %q", got.Description)
	}
	books, _ = store.ListBooks(author.ID)
	if len(books) != 2 {
		t.Fatalf("after refresh: %d books, want 2", len(books))
	}
	for _, b := range books {
		switch b.ForeignID {
		case "1":
			if b.Monitored {
				t.Error("refresh clobbered the user's unmonitored flag")
			}
		case "2":
			if !b.Monitored {
				t.Error("new book should inherit the author's monitored flag")
			}
		}
	}
}

func TestSyncBookCreatesStubAuthorAndEditions(t *testing.T) {
	svc, store, _ := newFixture(t)
	ctx := context.Background()

	book, err := svc.SyncBook(ctx, "1", true)
	if err != nil {
		t.Fatalf("SyncBook: %v", err)
	}
	author, err := store.GetAuthor(book.AuthorID)
	if err != nil {
		t.Fatalf("stub author missing: %v", err)
	}
	if author.Monitored {
		t.Error("stub author should be unmonitored")
	}

	editions, _ := store.ListEditions(book.ID)
	if len(editions) != 2 {
		t.Fatalf("%d editions, want 2", len(editions))
	}
	for _, e := range editions {
		wantMonitored := e.Format == library.FormatEbook
		if e.Monitored != wantMonitored {
			t.Errorf("edition %s (format %s): monitored = %v, want %v", e.ForeignID, e.Format, e.Monitored, wantMonitored)
		}
	}
}

// TestRefreshReconcilesStaleSeriesLink reproduces the "Artemis in Chance
// Assassin" bug: a book once linked to a series keeps that link forever, even
// after the provider corrects its data to a standalone — corrupting the
// organized path via {Series Title}. A refresh must drop the stale link.
func TestRefreshReconcilesStaleSeriesLink(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	// The book initially reports a (wrong) series membership.
	provider.books["1"].Series = []metadata.SeriesLink{{ForeignID: "7", Title: "Discworld", Position: 1}}
	book, err := svc.SyncBook(ctx, "1", true)
	if err != nil {
		t.Fatalf("SyncBook: %v", err)
	}
	if links, _ := store.ListSeriesForBook(book.ID); len(links) != 1 {
		t.Fatalf("expected 1 initial series link, got %+v", links)
	}

	// Provider corrects its data: the book is a standalone after all.
	provider.books["1"].Series = nil
	if err := svc.RefreshBook(ctx, book.ID); err != nil {
		t.Fatalf("RefreshBook: %v", err)
	}
	if links, _ := store.ListSeriesForBook(book.ID); len(links) != 0 {
		t.Errorf("stale series link not reconciled after refresh: %+v", links)
	}
}

func TestRefreshBookUpdatesEditions(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	book, err := svc.SyncBook(ctx, "1", true)
	if err != nil {
		t.Fatalf("SyncBook: %v", err)
	}

	provider.books["1"].Editions = append(provider.books["1"].Editions,
		metadata.Edition{ForeignID: "13", ISBN13: "9999999999999", Format: "ebook"})
	if err := svc.RefreshBook(ctx, book.ID); err != nil {
		t.Fatalf("RefreshBook: %v", err)
	}
	editions, _ := store.ListEditions(book.ID)
	if len(editions) != 3 {
		t.Errorf("%d editions after refresh, want 3", len(editions))
	}
}

func TestRefreshAllSkipsFailures(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	author, err := svc.SyncAuthor(ctx, "100", true)
	if err != nil {
		t.Fatal(err)
	}
	// A second author that the provider no longer knows about.
	ghost := &library.Author{Source: "fake", ForeignID: "666", Name: "Ghost Writer", Monitored: true}
	if err := store.UpsertAuthor(ghost); err != nil {
		t.Fatal(err)
	}
	provider.authors["100"].Description = "Refreshed."

	svc.RefreshAll(ctx) // must not abort on the ghost author

	got, _ := store.GetAuthor(author.ID)
	if got.Description != "Refreshed." {
		t.Error("healthy author was not refreshed after a failing one")
	}
}

// TestRefreshAllAbortsOnUnreachableStreak: when the provider stops
// responding partway through a sweep, RefreshAll gives up after a few
// consecutive unreachable results instead of timing out on every remaining
// author — a real outage in a library of hundreds would otherwise turn one
// background sweep into an hours-long stall.
func TestRefreshAllAbortsOnUnreachableStreak(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("bad%d", i)
		if err := store.UpsertAuthor(&library.Author{
			// Explicit SortName pins ListAuthors' iteration order (it sorts
			// by sort_name), independent of name-derivation rules.
			Source: "fake", ForeignID: id, Name: "Bad " + id, SortName: fmt.Sprintf("%04d", i), Monitored: true,
		}); err != nil {
			t.Fatal(err)
		}
		provider.forceUnreachable[id] = true
	}

	svc.RefreshAll(ctx)

	if len(provider.calls) != unreachableAbortThreshold {
		t.Fatalf("GetAuthor calls = %v (%d), want exactly %d — the breaker should have stopped the sweep",
			provider.calls, len(provider.calls), unreachableAbortThreshold)
	}
}

// TestRefreshAllUnreachableStreakResetsOnSuccess: an unreachable result that
// never strings together three IN A ROW must not abort the sweep — every
// record here alternates unreachable/not-found (an unrelated failure, which
// also resets the streak), so all five must be attempted.
func TestRefreshAllUnreachableStreakResetsOnSuccess(t *testing.T) {
	svc, store, provider := newFixture(t)
	ctx := context.Background()

	// Explicit SortName controls ListAuthors' iteration order (it sorts by
	// sort_name), independent of name-derivation rules.
	for i, unreachable := range []bool{true, false, true, false, true} {
		id := fmt.Sprintf("alt%d", i)
		if err := store.UpsertAuthor(&library.Author{
			Source: "fake", ForeignID: id, Name: id, SortName: fmt.Sprintf("%04d", i), Monitored: true,
		}); err != nil {
			t.Fatal(err)
		}
		if unreachable {
			provider.forceUnreachable[id] = true
		}
		// The "false" entries are simply absent from provider.authors, so
		// they fail with ErrNotFound — a different, streak-resetting error.
	}

	svc.RefreshAll(ctx)

	if len(provider.calls) != 5 {
		t.Fatalf("GetAuthor calls = %v (%d), want all 5 attempted — no streak ever reached the threshold",
			provider.calls, len(provider.calls))
	}
}

func TestRefreshAuthorNotFound(t *testing.T) {
	svc, _, _ := newFixture(t)
	if err := svc.RefreshAuthor(context.Background(), 42); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("err = %v, want library.ErrNotFound", err)
	}
}

// otherProvider is a second, distinctly-named metadata source used to test a
// per-record provider override — one whose ids share nothing with the
// "fake" provider newFixture wires up as the active/original one.
type otherProvider struct{}

func (otherProvider) Name() string { return "other" }

func (otherProvider) SearchAuthors(_ context.Context, query string) ([]metadata.Author, error) {
	if query != "Terry Pratchett" {
		return nil, nil
	}
	return []metadata.Author{{ForeignID: "other-author-9", Name: "Terry Pratchett"}}, nil
}

func (otherProvider) SearchBooks(_ context.Context, query string) ([]metadata.Book, error) {
	if query != "The Colour of Magic" {
		return nil, nil
	}
	return []metadata.Book{{
		ForeignID: "other-book-9", Title: "The Colour of Magic", AuthorName: "Terry Pratchett",
	}}, nil
}

func (otherProvider) GetAuthor(_ context.Context, id string) (*metadata.Author, error) {
	if id != "other-author-9" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.Author{
		ForeignID: "other-author-9", Name: "Terry Pratchett",
		Description: "Resolved via the override provider, by name.",
		Books: []metadata.Book{
			{ForeignID: "other-book-9", Title: "The Colour of Magic", AuthorForeignID: "other-author-9", AuthorName: "Terry Pratchett"},
		},
	}, nil
}

func (otherProvider) GetBook(_ context.Context, id string) (*metadata.Book, error) {
	if id != "other-book-9" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.Book{
		ForeignID: "other-book-9", Title: "The Colour of Magic", AuthorForeignID: "other-author-9",
		AuthorName: "Terry Pratchett", Description: "Resolved via the override provider, by name.",
	}, nil
}

// TestRefreshAuthorProviderOverrideResolvesFreshID: an author sourced from
// one provider, pinned via a per-record override to a DIFFERENT provider,
// must not blindly reuse the original provider's foreign id on refresh — it
// belongs to a different namespace ("100" means nothing to the override
// provider) and would just 404 there. The refresh must re-find the author by
// name on the override provider instead. Regression for the bug where
// switching a record's provider override always failed with "not found at
// metadata provider", confirmed live against Open Library.
func TestRefreshAuthorProviderOverrideResolvesFreshID(t *testing.T) {
	svc, store, _ := newFixture(t)
	metadata.Register("other", func(metadata.Settings) (metadata.Provider, error) {
		return otherProvider{}, nil
	})

	author := &library.Author{
		Source: "fake", ForeignID: "100", Name: "Terry Pratchett", Monitored: true,
	}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	// A book already on file under the original provider, sharing a title
	// with what the override provider's bibliography will report under a
	// completely different foreign id.
	book := &library.Book{
		AuthorID: author.ID, Source: "fake", ForeignID: "1",
		Title: "The Colour of Magic", Monitored: true,
	}
	if err := store.UpsertBook(book); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAuthorProviderOverride(author.ID, "other"); err != nil {
		t.Fatal(err)
	}

	if err := svc.RefreshAuthor(context.Background(), author.ID); err != nil {
		t.Fatalf("RefreshAuthor: %v", err)
	}

	// The bibliography book must have updated in place by title match, not
	// duplicated under the override provider's own foreign id.
	books, err := store.ListBooks(author.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 {
		t.Fatalf("books under author after override refresh = %d, want 1 (no duplicate): %+v", len(books), books)
	}
	if books[0].ID != book.ID {
		t.Errorf("bibliography book was duplicated instead of updated in place: original id %d, got id %d", book.ID, books[0].ID)
	}
	if books[0].Source != "other" || books[0].ForeignID != "other-book-9" {
		t.Errorf("bibliography book not updated to the override provider's identity: %+v", books[0])
	}

	got, err := store.GetAuthor(author.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "other" {
		t.Errorf("author.Source = %q, want %q (refreshed via the override provider)", got.Source, "other")
	}
	if got.Description != "Resolved via the override provider, by name." {
		t.Errorf("author not refreshed with the override provider's data: %+v", got)
	}

	// The row must have been updated in place, not duplicated: the new
	// (source, foreign_id) natural key must resolve back to the SAME id, and
	// no second row should exist.
	byNewKey, err := store.GetAuthorByForeignID("other", "other-author-9")
	if err != nil {
		t.Fatalf("GetAuthorByForeignID(other, other-author-9): %v", err)
	}
	if byNewKey.ID != author.ID {
		t.Errorf("override refresh created a duplicate author row: original id %d, new-key id %d", author.ID, byNewKey.ID)
	}
	all, err := store.ListAuthors()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("author count after override refresh = %d, want 1 (no duplicate)", len(all))
	}
}

// TestRefreshBookProviderOverrideResolvesFreshID is the RefreshBook
// equivalent: the book's own stored foreign id belongs to the original
// provider, not the author's override, so it must be re-found by title.
func TestRefreshBookProviderOverrideResolvesFreshID(t *testing.T) {
	svc, store, _ := newFixture(t)
	metadata.Register("other", func(metadata.Settings) (metadata.Provider, error) {
		return otherProvider{}, nil
	})

	author := &library.Author{
		Source: "fake", ForeignID: "100", Name: "Terry Pratchett", Monitored: true,
	}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAuthorProviderOverride(author.ID, "other"); err != nil {
		t.Fatal(err)
	}
	book := &library.Book{
		AuthorID: author.ID, Source: "fake", ForeignID: "1",
		Title: "The Colour of Magic", Monitored: true,
	}
	if err := store.UpsertBook(book); err != nil {
		t.Fatal(err)
	}

	if err := svc.RefreshBook(context.Background(), book.ID); err != nil {
		t.Fatalf("RefreshBook: %v", err)
	}

	got, err := store.GetBook(book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "Resolved via the override provider, by name." {
		t.Errorf("book not refreshed with the override provider's data: %+v", got)
	}
	// Must be the SAME row under the author, not a duplicate, and the author
	// itself (already resolved by RefreshBook's caller) must not have been
	// re-derived into a duplicate stub via the book's (now different) author
	// foreign id.
	if got.AuthorID != author.ID {
		t.Errorf("book.AuthorID = %d, want %d (the already-known author, not a duplicate stub)", got.AuthorID, author.ID)
	}
	byNewKey, err := store.GetBookByForeignID("other", "other-book-9")
	if err != nil {
		t.Fatalf("GetBookByForeignID(other, other-book-9): %v", err)
	}
	if byNewKey.ID != book.ID {
		t.Errorf("override refresh created a duplicate book row: original id %d, new-key id %d", book.ID, byNewKey.ID)
	}
	allAuthors, err := store.ListAuthors()
	if err != nil {
		t.Fatal(err)
	}
	if len(allAuthors) != 1 {
		t.Errorf("author count after book override refresh = %d, want 1 (no duplicate stub)", len(allAuthors))
	}
}
