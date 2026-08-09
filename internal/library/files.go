package library

import (
	"database/sql"
	"errors"
)

// RootFolder mirrors the root_folders table (managed by the rootfolder API);
// the scanner needs them to know where to look. Variant is "color"/"mono"
// for manga roots (the colorized/monochrome split) and "" for everything
// else — files scanned from a root inherit it.
type RootFolder struct {
	ID        int64  `json:"id"`
	MediaType string `json:"mediaType"`
	Path      string `json:"path"`
	Variant   string `json:"variant,omitempty"`
}

// BookFile is a file found on disk by a library scan. BookID is nil-like (0)
// when the scanner could not match it to a library book. For multi-file
// audiobooks, Path is the book's directory and Size the total of its audio
// files. Variant carries the manga colorized/monochrome distinction (""
// for non-manga files).
type BookFile struct {
	ID           int64  `json:"id"`
	RootFolderID int64  `json:"rootFolderId"`
	BookID       int64  `json:"bookId,omitempty"`
	MediaType    string `json:"mediaType"`
	Variant      string `json:"variant,omitempty"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Format       string `json:"format"`
	ModifiedAt   string `json:"modifiedAt"`
	AddedAt      string `json:"addedAt"`
	// Tracks lists the audio files inside a multi-file audiobook unit (whose
	// Path is the book folder). Not persisted — the API fills it on demand.
	Tracks []Track `json:"tracks,omitempty"`
}

// Track is one audio file inside a multi-file audiobook unit.
type Track struct {
	Name string `json:"name"` // path relative to the book folder
	Size int64  `json:"size"`
}

func (s *Store) ListRootFolders() ([]RootFolder, error) {
	rows, err := s.db.Query(`SELECT id, media_type, path, variant FROM root_folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := []RootFolder{}
	for rows.Next() {
		var f RootFolder
		if err := rows.Scan(&f.ID, &f.MediaType, &f.Path, &f.Variant); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// UpsertBookFile inserts or refreshes a file by path. The scanner owns the
// book match, so book_id is updated on re-scan (a file can gain a match after
// its book is added to the library).
func (s *Store) UpsertBookFile(f *BookFile) error {
	return upsertBookFile(s.db, f)
}

func upsertBookFile(db execer, f *BookFile) error {
	bookID := sql.NullInt64{Int64: f.BookID, Valid: f.BookID > 0}
	if f.MediaType == "" {
		f.MediaType = "ebook"
	}
	// book_id is COALESCEd so a re-scan that can't match a filename on its own
	// never clears an existing match — manual imports stay imported. Explicit
	// unmatching goes through SetBookFileBook.
	err := db.QueryRow(`
		INSERT INTO book_files (root_folder_id, book_id, media_type, variant, path, size, format, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			root_folder_id = excluded.root_folder_id,
			book_id = COALESCE(excluded.book_id, book_files.book_id),
			media_type = excluded.media_type,
			variant = excluded.variant,
			size = excluded.size,
			format = excluded.format,
			modified_at = excluded.modified_at
		RETURNING id, COALESCE(book_id, 0)`,
		f.RootFolderID, bookID, f.MediaType, f.Variant, f.Path, f.Size, f.Format, f.ModifiedAt,
	).Scan(&f.ID, &f.BookID)
	if err != nil {
		return err
	}
	// Owning a format puts the book in that format's library (Plex-style
	// explicit membership; monitored flag untouched).
	if f.BookID > 0 {
		return ensureBookLibrary(db, f.BookID, f.MediaType)
	}
	return nil
}

// BookFileBatch accumulates book_file writes (from a scan pass) inside one
// transaction instead of the usual one-commit-per-call — a scan can touch
// thousands of files, and one WAL commit instead of thousands turns a
// scan's dominant cost (fsync-per-write) into a non-issue. Call
// UpsertBookFile/DeleteBookFile as many times as needed, then Commit();
// Rollback is safe to call unconditionally afterward (a no-op post-Commit),
// so callers can `defer batch.Rollback()` right after BeginBookFileBatch.
type BookFileBatch struct {
	tx *sql.Tx
}

// BeginBookFileBatch starts a batch. The caller must Commit or Rollback.
func (s *Store) BeginBookFileBatch() (*BookFileBatch, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	return &BookFileBatch{tx: tx}, nil
}

func (b *BookFileBatch) UpsertBookFile(f *BookFile) error { return upsertBookFile(b.tx, f) }
func (b *BookFileBatch) DeleteBookFile(id int64) error    { return deleteBookFile(b.tx, id) }
func (b *BookFileBatch) Commit() error                    { return b.tx.Commit() }

// Rollback aborts the batch. Harmless to call after a successful Commit.
func (b *BookFileBatch) Rollback() { _ = b.tx.Rollback() }

const bookFileCols = `id, root_folder_id, COALESCE(book_id, 0), media_type, variant, path, size, format, modified_at, added_at`

func scanBookFile(row interface{ Scan(...any) error }) (*BookFile, error) {
	var f BookFile
	err := row.Scan(&f.ID, &f.RootFolderID, &f.BookID, &f.MediaType, &f.Variant, &f.Path, &f.Size, &f.Format, &f.ModifiedAt, &f.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *Store) listBookFiles(where string, args ...any) ([]BookFile, error) {
	rows, err := s.db.Query(`SELECT `+bookFileCols+` FROM book_files `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []BookFile{}
	for rows.Next() {
		f, err := scanBookFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, rows.Err()
}

func (s *Store) GetBookFile(id int64) (*BookFile, error) {
	return scanBookFile(s.db.QueryRow(`SELECT `+bookFileCols+` FROM book_files WHERE id = ?`, id))
}

func (s *Store) ListBookFiles(bookID int64) ([]BookFile, error) {
	return s.listBookFiles(`WHERE book_id = ? ORDER BY path`, bookID)
}

func (s *Store) ListMatchedBookFiles() ([]BookFile, error) {
	return s.listBookFiles(`WHERE book_id IS NOT NULL ORDER BY path`)
}

// ListMatchedBookFilesForAuthor returns the matched files of one author's
// books — the author-scoped Organize.
func (s *Store) ListMatchedBookFilesForAuthor(authorID int64) ([]BookFile, error) {
	return s.listBookFiles(
		`WHERE book_id IN (SELECT id FROM books WHERE author_id = ?) ORDER BY path`, authorID)
}

// ListMatchedBookFilesForSeries returns the matched files of one series'
// volumes/issues — the series-scoped Organize.
func (s *Store) ListMatchedBookFilesForSeries(seriesID int64) ([]BookFile, error) {
	return s.listBookFiles(
		`WHERE book_id IN (SELECT book_id FROM series_books WHERE series_id = ?) ORDER BY path`, seriesID)
}

func (s *Store) ListUnmatchedBookFiles() ([]BookFile, error) {
	return s.listBookFiles(`WHERE book_id IS NULL ORDER BY path`)
}

// SetBookFileBook assigns (or clears, with 0) a file's book — the manual
// import action.
func (s *Store) SetBookFileBook(id, bookID int64) error {
	b := sql.NullInt64{Int64: bookID, Valid: bookID > 0}
	res, err := s.db.Exec(`UPDATE book_files SET book_id = ? WHERE id = ?`, b, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if bookID > 0 {
		if file, err := s.GetBookFile(id); err == nil {
			return s.EnsureBookLibrary(bookID, file.MediaType)
		}
	}
	return nil
}

// SetBookFilePath records a file's new location after a rename/move.
func (s *Store) SetBookFilePath(id int64, path string) error {
	res, err := s.db.Exec(`UPDATE book_files SET path = ? WHERE id = ?`, path, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BookFilePathsUnderRoot returns path → id for every recorded file in a root
// folder, so the scanner can prune records whose files vanished from disk.
func (s *Store) BookFilePathsUnderRoot(rootFolderID int64) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT id, path FROM book_files WHERE root_folder_id = ?`, rootFolderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := map[string]int64{}
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		paths[path] = id
	}
	return paths, rows.Err()
}

// VolumeRef locates one manga volume / comic issue for scan matching.
type VolumeRef struct {
	BookID      int64
	Position    float64
	SeriesTitle string
	MediaType   string
}

// ListVolumeRefs returns every manga/comic volume with its series title and
// number — the scanner's matching index for comic-style roots.
func (s *Store) ListVolumeRefs() ([]VolumeRef, error) {
	rows, err := s.db.Query(`
		SELECT sb.book_id, sb.position, s.title, s.media_type
		FROM series_books sb
		JOIN series s ON s.id = sb.series_id
		WHERE s.media_type != 'book'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	refs := []VolumeRef{}
	for rows.Next() {
		var r VolumeRef
		if err := rows.Scan(&r.BookID, &r.Position, &r.SeriesTitle, &r.MediaType); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// FilePathsForBook returns the on-disk paths of a book's files (for
// multi-file audiobooks the path is the book's directory). Used by the
// delete-files option before the rows go away.
func (s *Store) FilePathsForBook(bookID int64) ([]string, error) {
	return s.filePaths(`SELECT path FROM book_files WHERE book_id = ?`, bookID)
}

// FilePathsForBookFormat returns a book's file paths for one media type —
// the delete-files option when leaving a single format library.
func (s *Store) FilePathsForBookFormat(bookID int64, mediaType string) ([]string, error) {
	return s.filePaths(`SELECT path FROM book_files WHERE book_id = ? AND media_type = ?`, bookID, mediaType)
}

// DeleteBookFilesForFormat forgets a book's file rows for one media type
// (used after their disk files are deleted; the book itself survives).
func (s *Store) DeleteBookFilesForFormat(bookID int64, mediaType string) error {
	_, err := s.db.Exec(`DELETE FROM book_files WHERE book_id = ? AND media_type = ?`, bookID, mediaType)
	return err
}

// FilePathsForAuthor returns the on-disk paths of every file attached to an
// author's books.
func (s *Store) FilePathsForAuthor(authorID int64) ([]string, error) {
	return s.filePaths(`
		SELECT f.path FROM book_files f
		JOIN books b ON b.id = f.book_id
		WHERE b.author_id = ?`, authorID)
}

// FilePathsForAuthorFormat returns the on-disk paths of one format's files
// across an author's books — the delete-files option when removing an
// author from a single format library.
func (s *Store) FilePathsForAuthorFormat(authorID int64, mediaType string) ([]string, error) {
	return s.filePaths(`
		SELECT f.path FROM book_files f
		JOIN books b ON b.id = f.book_id
		WHERE b.author_id = ? AND f.media_type = ?`, authorID, mediaType)
}

// FilePathsForSeries returns the on-disk paths of every file attached to a
// series' volumes/issues.
func (s *Store) FilePathsForSeries(seriesID int64) ([]string, error) {
	return s.filePaths(`
		SELECT path FROM book_files
		WHERE book_id IN (SELECT book_id FROM series_books WHERE series_id = ?)`, seriesID)
}

func (s *Store) filePaths(query string, args ...any) ([]string, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

func (s *Store) DeleteBookFile(id int64) error {
	return deleteBookFile(s.db, id)
}

func deleteBookFile(db execer, id int64) error {
	res, err := db.Exec(`DELETE FROM book_files WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
