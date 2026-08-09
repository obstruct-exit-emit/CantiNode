package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/librinode/librinode/internal/metadata"
)

// SeriesClient adapts the Hardcover book API to metadata.SeriesProvider for
// manga and comics: a Hardcover *series* is the manga/comic, and the books
// linked to it via book_series are its volumes/issues. Hardcover's series are
// messier than AniList's — spin-offs sit at position 0 and each numbered
// volume carries several editions (English/Japanese/collector's) — so
// GetSeries keeps the positioned volumes (dropping the position-0 extras),
// collapses each position to its richest edition, and only numbers
// sequentially when a series has no positive positions at all.
type SeriesClient struct {
	*Client
	mediaType string
	// Global metadata preferences (lower-cased; empty = no preference):
	// editions matching language, then country, win their volume's slot.
	language string
	country  string
}

// SeriesFactory builds the Hardcover manga series provider; it shares the
// book provider's token.
func SeriesFactory(s metadata.Settings) (metadata.SeriesProvider, error) {
	return seriesFactoryFor("manga", s)
}

// ComicSeriesFactory builds the Hardcover comic series provider — the same
// client and series handling, registered for the comic media type.
func ComicSeriesFactory(s metadata.Settings) (metadata.SeriesProvider, error) {
	return seriesFactoryFor("comic", s)
}

func seriesFactoryFor(mediaType string, s metadata.Settings) (metadata.SeriesProvider, error) {
	if s.Token == "" {
		return nil, metadata.ErrNotConfigured
	}
	return &SeriesClient{
		Client:    New(s.Token),
		mediaType: mediaType,
		language:  strings.ToLower(s.Language),
		country:   strings.ToLower(s.Country),
	}, nil
}

func (sc *SeriesClient) MediaType() string { return sc.mediaType }

func (sc *SeriesClient) SearchSeries(ctx context.Context, query string) ([]metadata.SeriesResult, error) {
	docs, err := sc.search(ctx, query, "Series")
	if err != nil {
		return nil, err
	}
	results := []metadata.SeriesResult{}
	for _, doc := range docs {
		var d struct {
			ID         flexID `json:"id"`
			Name       string `json:"name"`
			BooksCount int    `json:"books_count"`
		}
		if err := json.Unmarshal(doc, &d); err != nil || d.ID == "" {
			continue
		}
		results = append(results, metadata.SeriesResult{
			ForeignID:  string(d.ID),
			Title:      d.Name,
			IssueCount: d.BooksCount,
		})
	}
	return results, nil
}

const seriesQuery = `query Series($id: Int!) {
  series(where: {id: {_eq: $id}}, limit: 1) {
    id
    name
    description
    books_count
    author { name }
    book_series(order_by: {position: asc_nulls_last}) {
      position
      book {
        id title description release_date cached_image
        editions { language { language } country { name } }
      }
    }
  }
}`

func (sc *SeriesClient) GetSeries(ctx context.Context, foreignID string) (*metadata.SeriesResult, error) {
	id, err := strconv.Atoi(foreignID)
	if err != nil {
		return nil, fmt.Errorf("hardcover: invalid series id %q: %w", foreignID, metadata.ErrNotFound)
	}

	var env struct {
		Series []struct {
			ID          json.Number `json:"id"`
			Name        string      `json:"name"`
			Description string      `json:"description"`
			BooksCount  int         `json:"books_count"`
			Author      *struct {
				Name string `json:"name"`
			} `json:"author"`
			BookSeries []struct {
				Position float64 `json:"position"`
				Book     *struct {
					ID          json.Number     `json:"id"`
					Title       string          `json:"title"`
					Description string          `json:"description"`
					ReleaseDate string          `json:"release_date"`
					CachedImage json.RawMessage `json:"cached_image"`
					Editions    []struct {
						Language *struct {
							Language string `json:"language"`
						} `json:"language"`
						Country *struct {
							Name string `json:"name"`
						} `json:"country"`
					} `json:"editions"`
				} `json:"book"`
			} `json:"book_series"`
		} `json:"series"`
	}
	if err := sc.do(ctx, seriesQuery, map[string]any{"id": id}, &env); err != nil {
		return nil, err
	}
	if len(env.Series) == 0 {
		return nil, metadata.ErrNotFound
	}
	s := env.Series[0]

	// Keep only entries with a book, in the order Hardcover returned them.
	entries := []entry{}
	for _, bs := range s.BookSeries {
		if bs.Book == nil || bs.Book.ID.String() == "" {
			continue
		}
		var e entry
		e.pos = bs.Position
		e.book.ID = bs.Book.ID
		e.book.Title = bs.Book.Title
		e.book.Description = bs.Book.Description
		e.book.ReleaseDate = bs.Book.ReleaseDate
		e.book.CachedImage = bs.Book.CachedImage
		for _, ed := range bs.Book.Editions {
			if ed.Language != nil && ed.Language.Language != "" {
				e.languages = append(e.languages, strings.ToLower(ed.Language.Language))
			}
			if ed.Country != nil && ed.Country.Name != "" {
				e.countries = append(e.countries, strings.ToLower(ed.Country.Name))
			}
		}
		entries = append(entries, e)
	}

	// Hardcover series mix the real numbered volumes (position >= 1) with
	// spin-offs and alternate editions (position 0), and carry several
	// editions per volume — translations, reissues, box sets and omnibus
	// printings alongside the standard release. When the series has
	// positioned volumes, keep one edition per position (see betterEdition:
	// prefer the global language/country preferences, then the standard
	// release, then the richest description) and drop the position-0 extras;
	// a series with no positive positions is numbered sequentially.
	hasPositive := false
	for _, e := range entries {
		if e.pos >= 1 {
			hasPositive = true
			break
		}
	}

	var chosen []entry
	if hasPositive {
		byPos := map[float64]entry{}
		for _, e := range entries {
			if e.pos < 1 {
				continue
			}
			if cur, ok := byPos[e.pos]; !ok || sc.betterEdition(e, cur) {
				byPos[e.pos] = e
			}
		}
		positions := make([]float64, 0, len(byPos))
		for p := range byPos {
			positions = append(positions, p)
		}
		sort.Float64s(positions)
		for _, p := range positions {
			chosen = append(chosen, byPos[p])
		}
	} else {
		chosen = entries
	}

	result := &metadata.SeriesResult{
		ForeignID:   s.ID.String(),
		Title:       s.Name,
		Description: s.Description,
		IssueCount:  len(chosen),
	}
	if s.Author != nil {
		result.AuthorName = s.Author.Name
	}
	for i, e := range chosen {
		num := e.pos
		if !hasPositive {
			num = float64(i + 1)
		}
		cover := imageURL(e.book.CachedImage)
		if i == 0 {
			result.CoverURL = cover
		}
		result.Issues = append(result.Issues, metadata.Issue{
			ForeignID:   e.book.ID.String(),
			Number:      num,
			Title:       e.book.Title,
			Description: e.book.Description,
			CoverURL:    cover,
			ReleaseDate: e.book.ReleaseDate,
		})
	}
	return result, nil
}

// entry is one edition linked to a series at a given volume position.
type entry struct {
	pos  float64
	book struct {
		ID          json.Number
		Title       string
		Description string
		ReleaseDate string
		CachedImage json.RawMessage
	}
	// The book's editions' languages and countries (lower-cased) — what the
	// global metadata preferences match against.
	languages []string
	countries []string
}

// matchesPref reports whether any value contains the (lower-cased, non-empty)
// preference — Hardcover uses full names, sometimes compound ("Spanish;
// Castilian", "United States of America"), so containment beats equality.
func matchesPref(values []string, pref string) bool {
	if pref == "" {
		return false
	}
	for _, v := range values {
		if strings.Contains(v, pref) {
			return true
		}
	}
	return false
}

// editionMarkers flag a variant printing (reissue, box set, deluxe, foreign
// omnibus, one-shot) rather than the standard volume release. Hardcover puts
// these all at the same position, so we sniff the title and description — the
// reissue's own blurb usually announces itself ("now reissued in a collector's
// edition…"), which is exactly the long text we must not mistake for a synopsis.
var editionMarkers = []string{
	"reissue", "collector", "deluxe", "omnibus", "all-in-one", "all in one",
	"box set", "boxed set", "black edition", "special edition",
	"anniversary edition", "complete collection", "complete edition",
	"one-shot", "one shot",
}

func specialEdition(e entry) bool {
	s := strings.ToLower(e.book.Title + " " + e.book.Description)
	for _, kw := range editionMarkers {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// betterEdition reports whether a should represent the volume instead of b.
// Strict-to-lenient tiers, each falling through when the entries tie: the
// global language preference, then the standard release over reissues/box
// sets ("non-reissued if possible"), then the global country preference,
// then an edition that actually carries a description, then the richer
// description. Entries without edition data simply never win the preference
// tiers — providers degrade gracefully when the data is missing.
func (sc *SeriesClient) betterEdition(a, b entry) bool {
	if la, lb := matchesPref(a.languages, sc.language), matchesPref(b.languages, sc.language); la != lb {
		return la // a wins when only it matches the preferred language
	}
	if sa, sb := specialEdition(a), specialEdition(b); sa != sb {
		return !sa // a wins when it is the standard (non-special) edition
	}
	if ca, cb := matchesPref(a.countries, sc.country), matchesPref(b.countries, sc.country); ca != cb {
		return ca // a wins when only it matches the preferred country
	}
	if da, db := a.book.Description != "", b.book.Description != ""; da != db {
		return da // a wins when it has a description and b doesn't
	}
	return len(a.book.Description) > len(b.book.Description)
}
