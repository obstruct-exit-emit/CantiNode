package library

import (
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// execer is satisfied by both *sql.DB and *sql.Tx. Write helpers that a scan
// calls per-file (UpsertBookFile, EnsureBookLibrary, DeleteBookFile) take one
// of these instead of assuming s.db directly, so BookFileBatch (files.go) can
// run the very same logic inside one transaction — a scan touching thousands
// of files turns from one WAL commit per file into one commit total.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// SortName derives a "Last, First Middle" sort key from a display name.
func SortName(name string) string {
	parts := strings.Fields(name)
	if len(parts) < 2 {
		return name
	}
	last := parts[len(parts)-1]
	return last + ", " + strings.Join(parts[:len(parts)-1], " ")
}

// SortTitle strips a leading English article for sorting.
func SortTitle(title string) string {
	lower := strings.ToLower(title)
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, article) && len(title) > len(article) {
			return title[len(article):]
		}
	}
	return title
}

// --- Authors ---

// UpsertAuthor inserts the author or, when (source, foreign_id) already
// exists, refreshes its metadata without touching the user-owned monitored
// flag. The author's ID is set on return.
func (s *Store) UpsertAuthor(a *Author) error {
	if a.SortName == "" {
		a.SortName = SortName(a.Name)
	}
	// A caller that already knows the row (a refresh, keyed by internal id —
	// never a fresh add) updates it directly, source/foreign_id included:
	// refreshing through a per-record provider override changes the natural
	// (metadata_source, foreign_id) key, and upserting by that key would
	// create a second row instead of updating this one.
	if a.ID != 0 {
		_, err := s.db.Exec(`
			UPDATE authors SET metadata_source = ?, foreign_id = ?, name = ?, sort_name = ?,
				description = ?, image_url = ?, monitored = ?, updated_at = datetime('now')
			WHERE id = ?`,
			a.Source, a.ForeignID, a.Name, a.SortName, a.Description, a.ImageURL, a.Monitored, a.ID,
		)
		return err
	}
	return s.db.QueryRow(`
		INSERT INTO authors (metadata_source, foreign_id, name, sort_name, description, image_url, monitored)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (metadata_source, foreign_id) DO UPDATE SET
			name = excluded.name,
			sort_name = excluded.sort_name,
			description = excluded.description,
			image_url = excluded.image_url,
			updated_at = datetime('now')
		RETURNING id`,
		a.Source, a.ForeignID, a.Name, a.SortName, a.Description, a.ImageURL, a.Monitored,
	).Scan(&a.ID)
}

const authorCols = `id, metadata_source, foreign_id, name, sort_name, description, image_url, monitored,
	in_ebook_library, in_audiobook_library, provider_override, added_at, updated_at`

func scanAuthor(row interface{ Scan(...any) error }) (*Author, error) {
	var a Author
	err := row.Scan(&a.ID, &a.Source, &a.ForeignID, &a.Name, &a.SortName,
		&a.Description, &a.ImageURL, &a.Monitored,
		&a.InEbookLibrary, &a.InAudiobookLibrary, &a.ProviderOverride, &a.AddedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) GetAuthor(id int64) (*Author, error) {
	return scanAuthor(s.db.QueryRow(`SELECT `+authorCols+` FROM authors WHERE id = ?`, id))
}

func (s *Store) GetAuthorByForeignID(source, foreignID string) (*Author, error) {
	return scanAuthor(s.db.QueryRow(
		`SELECT `+authorCols+` FROM authors WHERE metadata_source = ? AND foreign_id = ?`,
		source, foreignID))
}

func (s *Store) ListAuthors() ([]Author, error) {
	rows, err := s.db.Query(`SELECT ` + authorCols + ` FROM authors ORDER BY sort_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	authors := []Author{}
	for rows.Next() {
		a, err := scanAuthor(rows)
		if err != nil {
			return nil, err
		}
		authors = append(authors, *a)
	}
	return authors, rows.Err()
}

// DeleteAuthor removes the author; books, editions, and series links cascade.
func (s *Store) DeleteAuthor(id int64) error {
	res, err := s.db.Exec(`DELETE FROM authors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearDescriptions blanks the stored author, book, and series descriptions
// so they're re-fetched on the next metadata refresh. Returns how many rows
// were cleared.
func (s *Store) ClearDescriptions() (int64, error) {
	var total int64
	for _, table := range []string{"authors", "books", "series"} {
		res, err := s.db.Exec(`UPDATE ` + table + ` SET description = '' WHERE description != ''`)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// SetAuthorLibrary adds or removes an author from a format library. Book
// membership is handled separately (SetAuthorBooksLibrary).
func (s *Store) SetAuthorLibrary(id int64, mediaType string, member bool) error {
	col, err := libraryColumn(mediaType)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE authors SET `+col+` = ?, updated_at = datetime('now') WHERE id = ?`, member, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ensureAuthorLibrary marks a book's author as a member of the format
// library — a book being deliberately added (or owned) there implies its
// author belongs there too.
func (s *Store) ensureAuthorLibrary(bookID int64, mediaType string) error {
	return ensureAuthorLibrary(s.db, bookID, mediaType)
}

func ensureAuthorLibrary(db execer, bookID int64, mediaType string) error {
	col, err := libraryColumn(mediaType)
	if err != nil {
		return nil // non-format media types have no author membership
	}
	_, err = db.Exec(
		`UPDATE authors SET `+col+` = 1 WHERE id = (SELECT author_id FROM books WHERE id = ?)`, bookID)
	return err
}

// libraryColumn maps a format library to its authors membership column.
func libraryColumn(mediaType string) (string, error) {
	switch mediaType {
	case "ebook":
		return "in_ebook_library", nil
	case "audiobook":
		return "in_audiobook_library", nil
	}
	return "", errors.New("library must be ebook or audiobook")
}

// SetAuthorProviderOverride pins (or with "" clears) the author's metadata
// provider override.
func (s *Store) SetAuthorProviderOverride(id int64, provider string) error {
	res, err := s.db.Exec(
		`UPDATE authors SET provider_override = ?, updated_at = datetime('now') WHERE id = ?`, provider, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetAuthorMonitored(id int64, monitored bool) error {
	res, err := s.db.Exec(
		`UPDATE authors SET monitored = ?, updated_at = datetime('now') WHERE id = ?`, monitored, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Books ---

// UpsertBook inserts or refreshes a book by (source, foreign_id), preserving
// the monitored flag on update. The book's ID is set on return.
func (s *Store) UpsertBook(b *Book) error {
	if b.SortTitle == "" {
		b.SortTitle = SortTitle(b.Title)
	}
	if b.MediaType == "" {
		b.MediaType = "book"
	}
	// A caller that already knows the row (a refresh, keyed by internal id —
	// never a fresh add) updates it directly, source/foreign_id included: see
	// UpsertAuthor. Library membership columns are left untouched here too,
	// matching the natural-key path below.
	if b.ID != 0 {
		_, err := s.db.Exec(`
			UPDATE books SET author_id = ?, metadata_source = ?, media_type = ?, foreign_id = ?,
				title = ?, sort_title = ?, description = ?, release_date = ?, rating = ?, cover_url = ?,
				updated_at = datetime('now')
			WHERE id = ?`,
			b.AuthorID, b.Source, b.MediaType, b.ForeignID, b.Title, b.SortTitle,
			b.Description, b.ReleaseDate, b.Rating, b.CoverURL, b.ID,
		)
		return err
	}
	return s.db.QueryRow(`
		INSERT INTO books (author_id, metadata_source, media_type, foreign_id, title, sort_title, description, release_date, rating, cover_url, monitored,
			in_ebook_library, ebook_monitored, in_audiobook_library, audiobook_monitored)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (metadata_source, foreign_id) DO UPDATE SET
			author_id = excluded.author_id,
			media_type = excluded.media_type,
			title = excluded.title,
			sort_title = excluded.sort_title,
			description = excluded.description,
			release_date = excluded.release_date,
			rating = excluded.rating,
			cover_url = excluded.cover_url,
			updated_at = datetime('now')
		RETURNING id`,
		b.AuthorID, b.Source, b.MediaType, b.ForeignID, b.Title, b.SortTitle,
		b.Description, b.ReleaseDate, b.Rating, b.CoverURL, b.Monitored,
		b.InEbookLibrary, b.EbookMonitored, b.InAudiobookLibrary, b.AudiobookMonitored,
	).Scan(&b.ID)
}

// SetBookLibrary adds or removes a prose book from a format library
// (ebook/audiobook) and sets that membership's monitored flag.
func (s *Store) SetBookLibrary(id int64, mediaType string, member, monitored bool) error {
	var query string
	switch mediaType {
	case "ebook":
		query = `UPDATE books SET in_ebook_library = ?, ebook_monitored = ?, updated_at = datetime('now')
			WHERE id = ? AND media_type = 'book'`
	case "audiobook":
		query = `UPDATE books SET in_audiobook_library = ?, audiobook_monitored = ?, updated_at = datetime('now')
			WHERE id = ? AND media_type = 'book'`
	default:
		return errors.New("library must be ebook or audiobook")
	}
	res, err := s.db.Exec(query, member, member && monitored, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if member {
		return s.ensureAuthorLibrary(id, mediaType)
	}
	return nil
}

// EnsureBookLibrary makes an owned book (and its author) a member of the
// format's library without touching monitored flags (scan/import: owning it
// puts it there).
func (s *Store) EnsureBookLibrary(id int64, mediaType string) error {
	return ensureBookLibrary(s.db, id, mediaType)
}

func ensureBookLibrary(db execer, id int64, mediaType string) error {
	var query string
	switch mediaType {
	case "ebook":
		query = `UPDATE books SET in_ebook_library = 1 WHERE id = ? AND media_type = 'book'`
	case "audiobook":
		query = `UPDATE books SET in_audiobook_library = 1 WHERE id = ? AND media_type = 'book'`
	default:
		return nil
	}
	if _, err := db.Exec(query, id); err != nil {
		return err
	}
	return ensureAuthorLibrary(db, id, mediaType)
}

// RemoveAuthorBooksLibrary takes all of an author's prose books out of a
// format library (membership and monitoring; the other format is untouched).
func (s *Store) RemoveAuthorBooksLibrary(authorID int64, mediaType string) error {
	var query string
	switch mediaType {
	case "ebook":
		query = `UPDATE books SET in_ebook_library = 0, ebook_monitored = 0 WHERE author_id = ? AND media_type = 'book'`
	case "audiobook":
		query = `UPDATE books SET in_audiobook_library = 0, audiobook_monitored = 0 WHERE author_id = ? AND media_type = 'book'`
	default:
		return errors.New("library must be ebook or audiobook")
	}
	_, err := s.db.Exec(query, authorID)
	return err
}

// DeleteAuthorBookFilesForFormat forgets the file records of one format for
// all of an author's books (used after their files were deleted from disk).
func (s *Store) DeleteAuthorBookFilesForFormat(authorID int64, mediaType string) error {
	_, err := s.db.Exec(`DELETE FROM book_files WHERE media_type = ?
		AND book_id IN (SELECT id FROM books WHERE author_id = ?)`, mediaType, authorID)
	return err
}

const bookCols = `id, author_id, metadata_source, media_type, foreign_id, title, sort_title, description, release_date, rating, cover_url, monitored,
	in_ebook_library, ebook_monitored, in_audiobook_library, audiobook_monitored,
	EXISTS(SELECT 1 FROM book_files WHERE book_files.book_id = books.id),
	EXISTS(SELECT 1 FROM book_files WHERE book_files.book_id = books.id AND book_files.media_type = 'ebook'),
	EXISTS(SELECT 1 FROM book_files WHERE book_files.book_id = books.id AND book_files.media_type = 'audiobook'),
	EXISTS(SELECT 1 FROM book_files WHERE book_files.book_id = books.id AND book_files.media_type = 'manga' AND book_files.variant = 'color'),
	EXISTS(SELECT 1 FROM book_files WHERE book_files.book_id = books.id AND book_files.media_type = 'manga' AND book_files.variant = 'mono'),
	added_at, updated_at`

func scanBook(row interface{ Scan(...any) error }) (*Book, error) {
	var b Book
	err := row.Scan(&b.ID, &b.AuthorID, &b.Source, &b.MediaType, &b.ForeignID, &b.Title, &b.SortTitle,
		&b.Description, &b.ReleaseDate, &b.Rating, &b.CoverURL, &b.Monitored,
		&b.InEbookLibrary, &b.EbookMonitored, &b.InAudiobookLibrary, &b.AudiobookMonitored,
		&b.HasFile, &b.HasEbookFile, &b.HasAudiobookFile, &b.HasColorFile, &b.HasMonoFile,
		&b.AddedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) GetBook(id int64) (*Book, error) {
	return scanBook(s.db.QueryRow(`SELECT `+bookCols+` FROM books WHERE id = ?`, id))
}

func (s *Store) GetBookByForeignID(source, foreignID string) (*Book, error) {
	return scanBook(s.db.QueryRow(
		`SELECT `+bookCols+` FROM books WHERE metadata_source = ? AND foreign_id = ?`,
		source, foreignID))
}

// ListBooks returns all books, or only an author's books when authorID > 0.
func (s *Store) ListBooks(authorID int64) ([]Book, error) {
	query := `SELECT ` + bookCols + ` FROM books`
	args := []any{}
	if authorID > 0 {
		query += ` WHERE author_id = ?`
		args = append(args, authorID)
	}
	query += ` ORDER BY sort_title`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, *b)
	}
	return books, rows.Err()
}

// ListBooksInLibrary returns a format library's member prose books —
// filtered server-side, unlike ListBooks(0)'s whole-database fetch. The
// library page's manual-match fallback (existing-file import) only ever
// needs its own format's books, not every book of every media type; at a
// few thousand books that difference is several megabytes of JSON the
// browser never uses.
func (s *Store) ListBooksInLibrary(mediaType string) ([]Book, error) {
	col, err := libraryColumn(mediaType)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT ` + bookCols + ` FROM books WHERE media_type = 'book' AND ` + col + ` = 1 ORDER BY sort_title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	books := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, *b)
	}
	return books, rows.Err()
}

func (s *Store) DeleteBook(id int64) error {
	res, err := s.db.Exec(`DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetBookMonitored(id int64, monitored bool) error {
	res, err := s.db.Exec(
		`UPDATE books SET monitored = ?, updated_at = datetime('now') WHERE id = ?`, monitored, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Editions ---

// UpsertEdition inserts or refreshes an edition by (source, foreign_id),
// preserving the monitored flag on update. The edition's ID is set on return.
func (s *Store) UpsertEdition(e *Edition) error {
	if e.Format == "" {
		e.Format = FormatUnknown
	}
	return s.db.QueryRow(`
		INSERT INTO editions (book_id, metadata_source, foreign_id, title, isbn13, asin, format, publisher, language, release_date, cover_url, monitored)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (metadata_source, foreign_id) DO UPDATE SET
			book_id = excluded.book_id,
			title = excluded.title,
			isbn13 = excluded.isbn13,
			asin = excluded.asin,
			format = excluded.format,
			publisher = excluded.publisher,
			language = excluded.language,
			release_date = excluded.release_date,
			cover_url = excluded.cover_url
		RETURNING id`,
		e.BookID, e.Source, e.ForeignID, e.Title, e.ISBN13, e.ASIN, e.Format,
		e.Publisher, e.Language, e.ReleaseDate, e.CoverURL, e.Monitored,
	).Scan(&e.ID)
}

const editionCols = `id, book_id, metadata_source, foreign_id, title, isbn13, asin, format, publisher, language, release_date, cover_url, monitored`

func scanEdition(row interface{ Scan(...any) error }) (*Edition, error) {
	var e Edition
	err := row.Scan(&e.ID, &e.BookID, &e.Source, &e.ForeignID, &e.Title, &e.ISBN13, &e.ASIN,
		&e.Format, &e.Publisher, &e.Language, &e.ReleaseDate, &e.CoverURL, &e.Monitored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *Store) ListEditions(bookID int64) ([]Edition, error) {
	rows, err := s.db.Query(
		`SELECT `+editionCols+` FROM editions WHERE book_id = ? ORDER BY format, release_date`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	editions := []Edition{}
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			return nil, err
		}
		editions = append(editions, *e)
	}
	return editions, rows.Err()
}

// EditionIdent is one edition's stable identifiers, for matching on-disk files
// by ISBN/ASIN rather than by parsed title.
type EditionIdent struct {
	BookID int64
	ISBN13 string
	ASIN   string
}

// EditionIdentifiers returns every edition that carries an ISBN-13 or an ASIN,
// in one query — the scanner builds an identifier→book index from it so a file
// whose name or embedded metadata names an ISBN matches its book outright,
// ahead of any title guessing.
func (s *Store) EditionIdentifiers() ([]EditionIdent, error) {
	rows, err := s.db.Query(
		`SELECT book_id, isbn13, asin FROM editions WHERE isbn13 != '' OR asin != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	idents := []EditionIdent{}
	for rows.Next() {
		var e EditionIdent
		if err := rows.Scan(&e.BookID, &e.ISBN13, &e.ASIN); err != nil {
			return nil, err
		}
		idents = append(idents, e)
	}
	return idents, rows.Err()
}

// --- Series ---

// UpsertSeries inserts or refreshes a series by (source, foreign_id),
// preserving the user-owned monitoring flags on update. The series' ID is
// set on return.
func (s *Store) UpsertSeries(sr *Series) error {
	if sr.MediaType == "" {
		sr.MediaType = "book"
	}
	return s.db.QueryRow(`
		INSERT INTO series (metadata_source, foreign_id, title, description, media_type, monitored, monitor_new, cover_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (metadata_source, foreign_id) DO UPDATE SET
			title = excluded.title,
			description = excluded.description,
			cover_url = excluded.cover_url
		RETURNING id`,
		sr.Source, sr.ForeignID, sr.Title, sr.Description,
		sr.MediaType, sr.Monitored, sr.MonitorNew, sr.CoverURL,
	).Scan(&sr.ID)
}

// RebindSeries moves an existing series row onto a different provider identity
// (metadata_source, foreign_id) in place, keeping its id, monitoring flags and
// series_books links so a provider switch doesn't change the series' URL or
// lose its monitored state. Any other series already occupying the target
// identity is removed first so the UNIQUE(metadata_source, foreign_id)
// constraint holds.
func (s *Store) RebindSeries(id int64, source, foreignID, title, description, coverURL string) error {
	if _, err := s.GetSeries(id); err != nil {
		return err
	}
	var conflictID int64
	err := s.db.QueryRow(
		`SELECT id FROM series WHERE metadata_source = ? AND foreign_id = ? AND id <> ?`,
		source, foreignID, id).Scan(&conflictID)
	switch {
	case err == nil:
		if derr := s.DeleteSeries(conflictID); derr != nil {
			return derr
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = s.db.Exec(
		`UPDATE series SET metadata_source = ?, foreign_id = ?, title = ?, description = ?, cover_url = ? WHERE id = ?`,
		source, foreignID, title, description, coverURL, id)
	return err
}

// BookHasFiles reports whether a book has any downloaded files, so a provider
// migration can keep owned volumes instead of deleting their file links.
func (s *Store) BookHasFiles(id int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM book_files WHERE book_id = ?`, id).Scan(&n)
	return n > 0, err
}

// MoveBookFiles reassigns every file of one book to another — used when a
// provider migration replaces an owned volume with the new provider's
// same-numbered volume, so the file follows instead of being orphaned.
func (s *Store) MoveBookFiles(fromID, toID int64) error {
	_, err := s.db.Exec(`UPDATE book_files SET book_id = ? WHERE book_id = ?`, toID, fromID)
	return err
}

// SeriesBookPositions maps each linked book to its series position (volume
// number), so a provider migration can match old volumes to new ones by number.
func (s *Store) SeriesBookPositions(seriesID int64) (map[int64]float64, error) {
	rows, err := s.db.Query(`SELECT book_id, position FROM series_books WHERE series_id = ?`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	positions := map[int64]float64{}
	for rows.Next() {
		var bookID int64
		var pos float64
		if err := rows.Scan(&bookID, &pos); err != nil {
			return nil, err
		}
		positions[bookID] = pos
	}
	return positions, rows.Err()
}

const seriesCols = `id, metadata_source, foreign_id, title, description, media_type, monitored, monitor_new, provider_override, cover_url`

func scanSeries(row interface{ Scan(...any) error }) (*Series, error) {
	var sr Series
	err := row.Scan(&sr.ID, &sr.Source, &sr.ForeignID, &sr.Title, &sr.Description,
		&sr.MediaType, &sr.Monitored, &sr.MonitorNew, &sr.ProviderOverride, &sr.CoverURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sr, nil
}

func (s *Store) GetSeries(id int64) (*Series, error) {
	return scanSeries(s.db.QueryRow(`SELECT `+seriesCols+` FROM series WHERE id = ?`, id))
}

// ListSeries returns series of a media type ("" = all).
func (s *Store) ListSeries(mediaType string) ([]Series, error) {
	query := `SELECT ` + seriesCols + `,
		(SELECT COUNT(*) FROM series_books sb WHERE sb.series_id = series.id),
		(SELECT COUNT(*) FROM series_books sb WHERE sb.series_id = series.id
			AND EXISTS (SELECT 1 FROM book_files f WHERE f.book_id = sb.book_id))
	FROM series`
	args := []any{}
	if mediaType != "" {
		query += ` WHERE media_type = ?`
		args = append(args, mediaType)
	}
	query += ` ORDER BY title`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Series{}
	for rows.Next() {
		var sr Series
		if err := rows.Scan(&sr.ID, &sr.Source, &sr.ForeignID, &sr.Title, &sr.Description,
			&sr.MediaType, &sr.Monitored, &sr.MonitorNew, &sr.ProviderOverride, &sr.CoverURL,
			&sr.ItemCount, &sr.OwnedCount); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// SetSeriesProviderOverride pins (or with "" clears) the series' metadata
// provider override.
func (s *Store) SetSeriesProviderOverride(id int64, provider string) error {
	res, err := s.db.Exec(`UPDATE series SET provider_override = ? WHERE id = ?`, provider, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSeriesMonitored updates a series' monitoring flags and mirrors the
// monitored flag onto its volumes (per-volume overrides can follow after).
func (s *Store) SetSeriesMonitored(id int64, monitored, monitorNew bool) error {
	res, err := s.db.Exec(`UPDATE series SET monitored = ?, monitor_new = ? WHERE id = ?`,
		monitored, monitorNew, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.Exec(`
		UPDATE books SET monitored = ?, updated_at = datetime('now')
		WHERE media_type != 'book'
		  AND id IN (SELECT book_id FROM series_books WHERE series_id = ?)`,
		monitored, id)
	return err
}

// ListVolumes returns a series' volume/issue books ordered by position.
func (s *Store) ListVolumes(seriesID int64) ([]Book, error) {
	rows, err := s.db.Query(`
		SELECT `+bookCols+` FROM books
		JOIN series_books sb ON sb.book_id = books.id
		WHERE sb.series_id = ?
		ORDER BY sb.position`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	volumes := []Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, *b)
	}
	return volumes, rows.Err()
}

// EnsureAuthorStub finds or creates a minimal author row (creator stubs for
// series, magazine publishers).
func (s *Store) EnsureAuthorStub(source, foreignID, name string) (*Author, error) {
	author, err := s.GetAuthorByForeignID(source, foreignID)
	if err == nil {
		return author, nil
	}
	author = &Author{Source: source, ForeignID: foreignID, Name: name, Monitored: false}
	if err := s.UpsertAuthor(author); err != nil {
		return nil, err
	}
	return author, nil
}

// CreateMagazineIssue materializes one magazine issue as a book row linked
// to its series. identifier is a date ("2026-07-04", "2026-07") or
// "issue-N"; releaseDate is set when the identifier is a date. Issues are
// created on grab (wanted) or on scan (owned, unmonitored).
func (s *Store) CreateMagazineIssue(series *Series, identifier string, monitored bool) (*Book, error) {
	author, err := s.EnsureAuthorStub(series.Source, "magazine:"+series.ForeignID, series.Title)
	if err != nil {
		return nil, err
	}
	releaseDate := ""
	if len(identifier) >= 7 && identifier[4] == '-' {
		releaseDate = identifier
	}
	book := &Book{
		AuthorID:    author.ID,
		Source:      series.Source,
		MediaType:   "magazine",
		ForeignID:   series.ForeignID + ":" + identifier,
		Title:       series.Title + " - " + identifier,
		ReleaseDate: releaseDate,
		Monitored:   monitored,
	}
	if err := s.UpsertBook(book); err != nil {
		return nil, err
	}
	if err := s.LinkBookSeries(book.ID, series.ID, 0); err != nil {
		return nil, err
	}
	return book, nil
}

// DeleteSeries removes a series and its volume/issue books (prose books in
// a series are never deleted this way — they belong to their author).
func (s *Store) DeleteSeries(id int64) error {
	if _, err := s.GetSeries(id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		DELETE FROM books WHERE media_type != 'book'
		  AND id IN (SELECT book_id FROM series_books WHERE series_id = ?)`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM series WHERE id = ?`, id)
	return err
}

func (s *Store) LinkBookSeries(bookID, seriesID int64, position float64) error {
	_, err := s.db.Exec(`
		INSERT INTO series_books (series_id, book_id, position)
		VALUES (?, ?, ?)
		ON CONFLICT (series_id, book_id) DO UPDATE SET position = excluded.position`,
		seriesID, bookID, position)
	return err
}

// SetBookSeries makes a prose book's series links exactly match the provider's
// current set: it removes any series_books row for the book whose series isn't
// in keep. Called after (re)linking so a metadata refresh self-heals a stale or
// wrong link — e.g. a standalone that a provider once mislabeled as part of a
// series, or a link left behind after the provider corrected its data. An empty
// keep removes all of the book's series links. (Only prose books flow through
// here; manga/comic volumes are linked on a separate path.)
func (s *Store) SetBookSeries(bookID int64, keep []int64) error {
	if len(keep) == 0 {
		_, err := s.db.Exec(`DELETE FROM series_books WHERE book_id = ?`, bookID)
		return err
	}
	placeholders := strings.Repeat(",?", len(keep))[1:]
	args := make([]any, 0, len(keep)+1)
	args = append(args, bookID)
	for _, id := range keep {
		args = append(args, id)
	}
	_, err := s.db.Exec(
		`DELETE FROM series_books WHERE book_id = ? AND series_id NOT IN (`+placeholders+`)`, args...)
	return err
}

func (s *Store) ListSeriesForBook(bookID int64) ([]SeriesLink, error) {
	rows, err := s.db.Query(`
		SELECT sb.series_id, s.title, sb.position
		FROM series_books sb JOIN series s ON s.id = sb.series_id
		WHERE sb.book_id = ? ORDER BY s.title`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []SeriesLink{}
	for rows.Next() {
		var l SeriesLink
		if err := rows.Scan(&l.SeriesID, &l.Title, &l.Position); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}
