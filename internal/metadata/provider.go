// Package metadata defines the pluggable metadata-provider interface and the
// provider-neutral types it returns. Hardcover implements it for books and
// audiobooks; manga and comic providers (AniList, ComicVine) slot in behind
// the SeriesProvider interface.
package metadata

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a foreign id does not exist at the provider.
var ErrNotFound = errors.New("metadata: not found")

// ErrNotConfigured is returned by handlers when no provider is set up
// (e.g. no Hardcover API token yet).
var ErrNotConfigured = errors.New("metadata: no provider configured")

// ErrUnreachable marks a Validate (or other call) failure as a connection
// problem — the provider didn't respond at all — rather than the request
// reaching it and being rejected (bad token, revoked key). Callers that want
// to tell "Hardcover is down" apart from "your token is wrong" check
// errors.Is(err, ErrUnreachable); providers wrap the transport-level error
// with it (see hardcover.Client.do).
var ErrUnreachable = errors.New("metadata: provider unreachable")

// Provider is a remote metadata source. Foreign ids are provider-scoped
// strings; Name() is stored alongside them as metadata_source.
type Provider interface {
	// Name is the stable identifier persisted in metadata_source columns.
	Name() string
	// SearchAuthors returns authors matching a free-text query.
	SearchAuthors(ctx context.Context, query string) ([]Author, error)
	// SearchBooks returns books matching a free-text query.
	SearchBooks(ctx context.Context, query string) ([]Book, error)
	// GetAuthor returns one author with their Books populated
	// (books carry series links but not editions).
	GetAuthor(ctx context.Context, foreignID string) (*Author, error)
	// GetBook returns one book with Editions and Series populated.
	GetBook(ctx context.Context, foreignID string) (*Book, error)
}

type Author struct {
	ForeignID   string `json:"foreignAuthorId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ImageURL    string `json:"imageUrl"`
	BookCount   int    `json:"bookCount,omitempty"`
	Books       []Book `json:"books,omitempty"`
	// Source names the provider this record actually came from. A single
	// provider leaves it blank (the caller records its Name()); the fallback
	// chain stamps it so a record found through a fallback is persisted under
	// that fallback's name — so a later refresh routes back to the same
	// provider instead of the primary that never had it. See FallbackProvider.
	Source string `json:"metadataSource,omitempty"`
}

type Book struct {
	ForeignID       string       `json:"foreignBookId"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	ReleaseDate     string       `json:"releaseDate"`
	Rating          float64      `json:"rating"`
	CoverURL        string       `json:"coverUrl"`
	AuthorForeignID string       `json:"foreignAuthorId"`
	AuthorName      string       `json:"authorName"`
	Series          []SeriesLink `json:"series,omitempty"`
	Editions        []Edition    `json:"editions,omitempty"`
	// Source names the origin provider; see Author.Source.
	Source string `json:"metadataSource,omitempty"`
}

type SeriesLink struct {
	ForeignID   string  `json:"foreignSeriesId"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Position    float64 `json:"position"`
}

// SeriesProvider is a series-first metadata source (manga, comics): search
// series, then fetch one with its volumes/issues. AniList and ComicVine are
// the first implementations.
type SeriesProvider interface {
	// Name is stored in metadata_source columns.
	Name() string
	// MediaType is the library type this provider serves: manga or comic.
	MediaType() string
	// SearchSeries returns series matching a free-text query.
	SearchSeries(ctx context.Context, query string) ([]SeriesResult, error)
	// GetSeries returns one series with Issues populated.
	GetSeries(ctx context.Context, foreignID string) (*SeriesResult, error)
}

// SeriesResult is a manga/comic series at the provider.
type SeriesResult struct {
	ForeignID   string  `json:"foreignSeriesId"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	AuthorName  string  `json:"authorName,omitempty"` // writer / mangaka
	Year        int     `json:"year,omitempty"`
	CoverURL    string  `json:"coverUrl,omitempty"`
	IssueCount  int     `json:"issueCount"`
	Issues      []Issue `json:"issues,omitempty"` // populated by GetSeries
}

// Issue is one volume (manga) or issue (comic) of a series.
type Issue struct {
	ForeignID   string  `json:"foreignIssueId"`
	Number      float64 `json:"number"`
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	CoverURL    string  `json:"coverUrl,omitempty"`
	ReleaseDate string  `json:"releaseDate,omitempty"`
}

// Edition formats use the same strings as the library package:
// ebook, audiobook, physical, unknown.
type Edition struct {
	ForeignID   string `json:"foreignEditionId"`
	Title       string `json:"title"`
	ISBN13      string `json:"isbn13"`
	ASIN        string `json:"asin"`
	Format      string `json:"format"`
	Publisher   string `json:"publisher"`
	Language    string `json:"language"`
	ReleaseDate string `json:"releaseDate"`
	CoverURL    string `json:"coverUrl"`
}
