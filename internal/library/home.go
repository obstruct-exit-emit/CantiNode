package library

import (
	"database/sql"
	"sort"
)

// LibraryStatus describes one media-type library for the Plex-style UI: a
// library is active — and visible — only once the user created it by adding
// a root folder.
type LibraryStatus struct {
	MediaType string `json:"mediaType"`
	Active    bool   `json:"active"`
	Items     int    `json:"items"`
	Wanted    int    `json:"wanted"`
}

// MediaTypes in display order.
var MediaTypes = []string{"ebook", "audiobook", "manga", "comic", "magazine"}

// itemsWhere returns the SQL predicate selecting a library's *visible*
// items. Membership alone decides visibility: any book enrolled in a format
// library shows there, monitored or not, owned or not. Monitoring only gates
// automatic grabbing/upgrading (see wantedWhere) — it never hides a book.
// The Missing section is the complement: bibliography books NOT yet enrolled.
func itemsWhere(mediaType string) string {
	switch mediaType {
	case "ebook":
		return `books.media_type = 'book' AND books.in_ebook_library = 1`
	case "audiobook":
		return `books.media_type = 'book' AND books.in_audiobook_library = 1`
	}
	return `books.media_type = '` + mediaType + `'`
}

// wantedWhere returns the SQL predicate selecting a library's wanted items
// (monitored, missing their format's file).
func wantedWhere(mediaType string) string {
	fileClause := func(mt string) string {
		return `NOT EXISTS (SELECT 1 FROM book_files f WHERE f.book_id = books.id AND f.media_type = '` + mt + `')`
	}
	switch mediaType {
	case "ebook":
		return itemsWhere("ebook") + ` AND books.ebook_monitored = 1 AND ` + fileClause("ebook")
	case "audiobook":
		return itemsWhere("audiobook") + ` AND books.audiobook_monitored = 1 AND ` + fileClause("audiobook")
	}
	return itemsWhere(mediaType) + ` AND books.monitored = 1 AND ` + fileClause(mediaType)
}

// LibraryStatuses reports every media type's activity and counts. A library
// is active — and visible in the UI — only once the user has created it by
// adding a root folder (Plex-style: creating the library is an explicit act;
// content alone never surfaces one).
func (s *Store) LibraryStatuses() ([]LibraryStatus, error) {
	roots, err := s.ListRootFolders()
	if err != nil {
		return nil, err
	}
	hasRoot := map[string]bool{}
	for _, r := range roots {
		hasRoot[r.MediaType] = true
	}

	statuses := []LibraryStatus{}
	for _, mt := range MediaTypes {
		st := LibraryStatus{MediaType: mt}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM books WHERE ` + itemsWhere(mt)).Scan(&st.Items); err != nil {
			return nil, err
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM books WHERE ` + wantedWhere(mt)).Scan(&st.Wanted); err != nil {
			return nil, err
		}
		// Magazines count series, not materialized issues, as their items.
		if mt == "magazine" {
			var seriesCount int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM series WHERE media_type = 'magazine'`).Scan(&seriesCount); err != nil {
				return nil, err
			}
			if seriesCount > st.Items {
				st.Items = seriesCount
			}
		}
		st.Active = hasRoot[mt]
		statuses = append(statuses, st)
	}
	return statuses, nil
}

// HomeItem is one entry in a Home page row. AuthorID and SeriesID let the UI
// link the tile to its detail page: prose books open the book page (which
// needs the author for its back-navigation), volumes/issues open their series.
type HomeItem struct {
	BookID         int64   `json:"bookId"`
	AuthorID       int64   `json:"authorId,omitempty"`
	SeriesID       int64   `json:"seriesId,omitempty"`
	Title          string  `json:"title"`
	Subtitle       string  `json:"subtitle,omitempty"` // author or series
	CoverURL       string  `json:"coverUrl,omitempty"`
	HasFile        bool    `json:"hasFile"`
	ReleaseDate    string  `json:"releaseDate,omitempty"`
	Rating         float64 `json:"rating,omitempty"`
	SeriesTitle    string  `json:"seriesTitle,omitempty"`
	SeriesPosition float64 `json:"seriesPosition,omitempty"`
}

// HomeSection is one library's block on the Home page — rows never mix
// media types.
type HomeSection struct {
	MediaType     string     `json:"mediaType"`
	Items         int        `json:"items"`
	WantedCount   int        `json:"wantedCount"`
	RecentlyAdded []HomeItem `json:"recentlyAdded"`
	Wanted        []HomeItem `json:"wanted"`
}

func (s *Store) homeItems(where, order string, limit int, mediaType string) ([]HomeItem, error) {
	fileMT := mediaType
	rows, err := s.db.Query(`
		SELECT books.id, COALESCE(books.author_id, 0),
			COALESCE((SELECT sb.series_id FROM series_books sb WHERE sb.book_id = books.id LIMIT 1), 0),
			books.title, COALESCE(a.name, ''), books.cover_url,
			EXISTS (SELECT 1 FROM book_files f WHERE f.book_id = books.id AND f.media_type = '`+fileMT+`'),
			books.release_date, books.rating,
			COALESCE((SELECT s.title FROM series_books sb JOIN series s ON s.id = sb.series_id
				WHERE sb.book_id = books.id LIMIT 1), ''),
			COALESCE((SELECT sb.position FROM series_books sb WHERE sb.book_id = books.id LIMIT 1), 0)
		FROM books LEFT JOIN authors a ON a.id = books.author_id
		WHERE `+where+` ORDER BY `+order+` LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []HomeItem{}
	for rows.Next() {
		var it HomeItem
		if err := rows.Scan(&it.BookID, &it.AuthorID, &it.SeriesID, &it.Title, &it.Subtitle, &it.CoverURL, &it.HasFile,
			&it.ReleaseDate, &it.Rating, &it.SeriesTitle, &it.SeriesPosition); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// Home builds the per-library sections for active libraries only.
func (s *Store) Home(limit int) ([]HomeSection, error) {
	statuses, err := s.LibraryStatuses()
	if err != nil {
		return nil, err
	}
	sections := []HomeSection{}
	for _, st := range statuses {
		if !st.Active {
			continue
		}
		section := HomeSection{MediaType: st.MediaType, Items: st.Items, WantedCount: st.Wanted}
		if section.RecentlyAdded, err = s.homeItems(
			itemsWhere(st.MediaType), "books.added_at DESC, books.id DESC", limit, st.MediaType); err != nil {
			return nil, err
		}
		if section.Wanted, err = s.homeItems(
			wantedWhere(st.MediaType), "books.added_at DESC, books.id DESC", limit, st.MediaType); err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	return sections, nil
}

// Wanted lists a library's wanted items (monitored, missing their format's
// file) — the per-library Wanted page. Newest additions first.
func (s *Store) Wanted(mediaType string) ([]HomeItem, error) {
	return s.homeItems(wantedWhere(mediaType), "books.added_at DESC, books.id DESC", 1000, mediaType)
}

// CalendarItem is one dated entry on the calendar page. AuthorID (prose) and
// SeriesID (volumes/issues) let the UI open the item's page.
type CalendarItem struct {
	BookID      int64  `json:"bookId"`
	AuthorID    int64  `json:"authorId,omitempty"`
	SeriesID    int64  `json:"seriesId,omitempty"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	MediaType   string `json:"mediaType"`
	ReleaseDate string `json:"releaseDate"`
	Owned       bool   `json:"owned"`
}

// Calendar returns items visible in any library whose release date falls in
// [from, to] (ISO dates), across all media types, ordered by date.
func (s *Store) Calendar(from, to string) ([]CalendarItem, error) {
	items := []CalendarItem{}
	for _, mt := range MediaTypes {
		rows, err := s.db.Query(`
			SELECT books.id, books.title, COALESCE(a.name, ''), books.release_date,
				EXISTS (SELECT 1 FROM book_files f WHERE f.book_id = books.id AND f.media_type = '`+mt+`'),
				COALESCE(books.author_id, 0),
				COALESCE((SELECT sb.series_id FROM series_books sb WHERE sb.book_id = books.id LIMIT 1), 0)
			FROM books LEFT JOIN authors a ON a.id = books.author_id
			WHERE `+itemsWhere(mt)+` AND books.release_date >= ? AND books.release_date <= ?
			ORDER BY books.release_date`, from, to)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			it := CalendarItem{MediaType: mt}
			if err := rows.Scan(&it.BookID, &it.Title, &it.Subtitle, &it.ReleaseDate, &it.Owned,
				&it.AuthorID, &it.SeriesID); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ReleaseDate != items[j].ReleaseDate {
			return items[i].ReleaseDate < items[j].ReleaseDate
		}
		return items[i].Title < items[j].Title
	})
	return items, nil
}

// MissingForAuthor lists an author's bibliography gaps for one format
// library: prose books NOT yet enrolled in it (the complement of the visible
// library grid). Ordered for the Missing view — series first
// (alphabetical, by position within), then standalones by release date.
// Each book carries at most one series link for grouping.
func (s *Store) MissingForAuthor(authorID int64, mediaType string) ([]Book, error) {
	rows, err := s.db.Query(`
		SELECT `+bookCols+primarySeriesCols+`
		FROM books
		WHERE books.author_id = ? AND books.media_type = 'book' AND NOT (`+itemsWhere(mediaType)+`)
		ORDER BY (series_title = ''), series_title, series_pos, books.release_date, books.sort_title`,
		authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := []Book{}
	for rows.Next() {
		b, err := scanBookWithSeries(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, *b)
	}
	return books, rows.Err()
}

// primarySeriesCols are the two trailing SELECT expressions that carry a book's
// primary series (title + this book's position in it) alongside bookCols, for
// scanBookWithSeries. The series is chosen deterministically (first by title).
const primarySeriesCols = `,
	COALESCE((SELECT se.title FROM series_books sb JOIN series se ON se.id = sb.series_id
		WHERE sb.book_id = books.id ORDER BY se.title LIMIT 1), '') AS series_title,
	COALESCE((SELECT sb.position FROM series_books sb JOIN series se ON se.id = sb.series_id
		WHERE sb.book_id = books.id ORDER BY se.title LIMIT 1), 0) AS series_pos`

// scanBookWithSeries scans a row of bookCols + primarySeriesCols into a Book,
// attaching the primary series link when the book has one.
func scanBookWithSeries(rows *sql.Rows) (*Book, error) {
	var b Book
	var st string
	var sp float64
	if err := rows.Scan(&b.ID, &b.AuthorID, &b.Source, &b.MediaType, &b.ForeignID, &b.Title, &b.SortTitle,
		&b.Description, &b.ReleaseDate, &b.Rating, &b.CoverURL, &b.Monitored,
		&b.InEbookLibrary, &b.EbookMonitored, &b.InAudiobookLibrary, &b.AudiobookMonitored,
		&b.HasFile, &b.HasEbookFile, &b.HasAudiobookFile, &b.HasColorFile, &b.HasMonoFile,
		&b.AddedAt, &b.UpdatedAt, &st, &sp); err != nil {
		return nil, err
	}
	if st != "" {
		b.Series = []SeriesLink{{Title: st, Position: sp}}
	}
	return &b, nil
}

// ListAuthorBooks returns all of an author's prose books with their primary
// series attached, so the author page can group/sort the grid by series (plain
// ListBooks omits series for speed). Ordered by title; the UI re-sorts.
func (s *Store) ListAuthorBooks(authorID int64) ([]Book, error) {
	rows, err := s.db.Query(`SELECT `+bookCols+primarySeriesCols+`
		FROM books WHERE books.author_id = ? AND books.media_type = 'book'
		ORDER BY sort_title`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := []Book{}
	for rows.Next() {
		b, err := scanBookWithSeries(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, *b)
	}
	return books, rows.Err()
}

// ListAuthorsInLibrary returns authors with at least one book in the given
// format library, with per-author totals for the grid cards (books in this
// library, and how many are owned in this format).
func (s *Store) ListAuthorsInLibrary(mediaType string) ([]Author, error) {
	rows, err := s.db.Query(`
		SELECT ` + authorCols + `,
			(SELECT COUNT(*) FROM books WHERE books.author_id = authors.id AND ` + itemsWhere(mediaType) + `),
			(SELECT COUNT(*) FROM books WHERE books.author_id = authors.id AND ` + itemsWhere(mediaType) + `
				AND EXISTS (SELECT 1 FROM book_files f WHERE f.book_id = books.id AND f.media_type = '` + mediaType + `'))
		FROM authors
		WHERE authors.` + authorMemberCol(mediaType) + ` = 1
			OR EXISTS (SELECT 1 FROM books WHERE books.author_id = authors.id AND ` + itemsWhere(mediaType) + `)
		ORDER BY sort_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	authors := []Author{}
	for rows.Next() {
		var a Author
		if err := rows.Scan(&a.ID, &a.Source, &a.ForeignID, &a.Name, &a.SortName,
			&a.Description, &a.ImageURL, &a.Monitored,
			&a.InEbookLibrary, &a.InAudiobookLibrary, &a.ProviderOverride, &a.AddedAt, &a.UpdatedAt,
			&a.BookCount, &a.OwnedCount); err != nil {
			return nil, err
		}
		authors = append(authors, a)
	}
	return authors, rows.Err()
}

// authorMemberCol maps a format library to the authors membership column.
func authorMemberCol(mediaType string) string {
	if mediaType == "audiobook" {
		return "in_audiobook_library"
	}
	return "in_ebook_library"
}
