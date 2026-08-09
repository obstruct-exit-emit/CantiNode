package metadata

import (
	"context"
	"errors"
)

// FallbackProvider chains a primary book provider with ordered fallbacks:
// the primary answers everything it can, and a fallback is consulted only
// when the primary comes up empty — the "as fallbacks" contract, not a
// merge. It matters most for search (find a book the primary doesn't carry)
// and for the add that follows (resolve that book's id through the same
// chain).
//
// Every record a fallback returns is stamped with that fallback's Name() in
// Source, so persistence records the true origin. A later refresh then routes
// straight to the origin provider by source name (Manager.ProviderByName),
// never back through the primary that never had the record — the reason the
// Source field exists on Author/Book.
type FallbackProvider struct {
	primary   Provider
	fallbacks []Provider
}

// NewFallback wraps primary with ordered fallbacks. Nil entries are dropped;
// with no usable fallback it returns the primary unchanged, so callers can
// use it unconditionally.
func NewFallback(primary Provider, fallbacks ...Provider) Provider {
	usable := make([]Provider, 0, len(fallbacks))
	for _, f := range fallbacks {
		if f != nil && f.Name() != primary.Name() {
			usable = append(usable, f)
		}
	}
	if len(usable) == 0 {
		return primary
	}
	return &FallbackProvider{primary: primary, fallbacks: usable}
}

func (f *FallbackProvider) Name() string { return f.primary.Name() }

// chain is the primary followed by the fallbacks, in consult order.
func (f *FallbackProvider) chain() []Provider {
	return append([]Provider{f.primary}, f.fallbacks...)
}

func (f *FallbackProvider) SearchAuthors(ctx context.Context, query string) ([]Author, error) {
	authors, err := f.primary.SearchAuthors(ctx, query)
	if err == nil && len(authors) > 0 {
		return authors, nil
	}
	for _, p := range f.fallbacks {
		alt, altErr := p.SearchAuthors(ctx, query)
		if altErr == nil && len(alt) > 0 {
			for i := range alt {
				alt[i].Source = p.Name()
			}
			return alt, nil
		}
	}
	// The primary's result (empty list, or its error) stands when nothing
	// else turned anything up — its error is the more useful one to surface.
	return authors, err
}

func (f *FallbackProvider) SearchBooks(ctx context.Context, query string) ([]Book, error) {
	books, err := f.primary.SearchBooks(ctx, query)
	if err == nil && len(books) > 0 {
		return books, nil
	}
	for _, p := range f.fallbacks {
		alt, altErr := p.SearchBooks(ctx, query)
		if altErr == nil && len(alt) > 0 {
			for i := range alt {
				alt[i].Source = p.Name()
			}
			return alt, nil
		}
	}
	return books, err
}

// GetAuthor resolves an id through the chain: the primary first, then each
// fallback on a not-found. Only a not-found falls through — a real transport
// error from the provider that owns the id should surface, not be masked by
// trying a provider that never had it.
func (f *FallbackProvider) GetAuthor(ctx context.Context, foreignID string) (*Author, error) {
	var firstErr error
	for _, p := range f.chain() {
		a, err := p.GetAuthor(ctx, foreignID)
		if err == nil {
			if a.Source == "" {
				a.Source = p.Name()
			}
			return a, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, firstErr
}

func (f *FallbackProvider) GetBook(ctx context.Context, foreignID string) (*Book, error) {
	var firstErr error
	for _, p := range f.chain() {
		b, err := p.GetBook(ctx, foreignID)
		if err == nil {
			if b.Source == "" {
				b.Source = p.Name()
			}
			return b, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, firstErr
}

// Validate delegates to the primary when it can validate — the fallbacks are
// keyless, so the primary's credentials are the only ones worth a live check.
func (f *FallbackProvider) Validate(ctx context.Context) error {
	if v, ok := f.primary.(Validator); ok {
		return v.Validate(ctx)
	}
	return nil
}
