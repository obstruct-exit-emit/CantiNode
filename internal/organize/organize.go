// Package organize moves matched book files into their template-defined
// locations: <root>/<folder template>/<file template>.<ext>. Plans are
// computed separately from application so the UI can preview before touching
// disk.
package organize

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/naming"
)

type Service struct {
	store *library.Store
	cfg   *config.Config
}

func New(store *library.Store, cfg *config.Config) *Service {
	return &Service{store: store, cfg: cfg}
}

// Move is one planned (or applied) file relocation.
type Move struct {
	FileID    int64  `json:"fileId"`
	BookID    int64  `json:"bookId"`
	BookTitle string `json:"bookTitle"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Plan computes the moves needed to bring matched files in line with the
// naming templates — for one book (bookID > 0) or the whole library. Files
// already in place produce no move. Problems with individual files (missing
// book, vanished root) are reported as skips, not errors.
func (s *Service) Plan(bookID int64) ([]Move, []string, error) {
	var files []library.BookFile
	var err error
	if bookID > 0 {
		files, err = s.store.ListBookFiles(bookID)
	} else {
		files, err = s.store.ListMatchedBookFiles()
	}
	if err != nil {
		return nil, nil, err
	}
	return s.planFiles(files)
}

// PlanAuthor is Plan scoped to one author's files (the author page's
// Organize button — nobody else's files move).
func (s *Service) PlanAuthor(authorID int64) ([]Move, []string, error) {
	files, err := s.store.ListMatchedBookFilesForAuthor(authorID)
	if err != nil {
		return nil, nil, err
	}
	return s.planFiles(files)
}

// PlanSeries is Plan scoped to one series' volume/issue files (the series
// page's Organize button).
func (s *Service) PlanSeries(seriesID int64) ([]Move, []string, error) {
	files, err := s.store.ListMatchedBookFilesForSeries(seriesID)
	if err != nil {
		return nil, nil, err
	}
	return s.planFiles(files)
}

// PlanLibrary is Plan scoped to ONE library's files (a library page's
// Organize button) — the other media types' files never move.
func (s *Service) PlanLibrary(mediaType string) ([]Move, []string, error) {
	files, err := s.store.ListMatchedBookFiles()
	if err != nil {
		return nil, nil, err
	}
	mine := files[:0]
	for _, f := range files {
		if f.MediaType == mediaType {
			mine = append(mine, f)
		}
	}
	return s.planFiles(mine)
}

func (s *Service) planFiles(files []library.BookFile) ([]Move, []string, error) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return nil, nil, err
	}
	rootByID := map[int64]string{}
	for _, r := range roots {
		rootByID[r.ID] = r.Path
	}

	moves := []Move{}
	skips := []string{}
	for _, f := range files {
		target, title, err := s.targetPath(&f, rootByID)
		if err != nil {
			skips = append(skips, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		if sameFile(f.Path, target) {
			continue
		}
		moves = append(moves, Move{
			FileID:    f.ID,
			BookID:    f.BookID,
			BookTitle: title,
			From:      f.Path,
			To:        target,
		})
	}
	return moves, skips, nil
}

// Apply executes planned moves: create the target directory, rename the
// file, record the new path, and sweep now-empty source directories (never
// the root folder itself). Moves whose target already exists are skipped
// (never overwrite).
func (s *Service) Apply(moves []Move) (applied []Move, skips []string, err error) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return nil, nil, err
	}

	applied = []Move{}
	skips = []string{}
	for _, m := range moves {
		if _, err := os.Stat(m.To); err == nil {
			skips = append(skips, fmt.Sprintf("%s: target already exists", m.To))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(m.To), 0o755); err != nil {
			skips = append(skips, fmt.Sprintf("%s: %v", m.To, err))
			continue
		}
		if err := os.Rename(m.From, m.To); err != nil {
			skips = append(skips, fmt.Sprintf("%s: %v", m.From, err))
			continue
		}
		if info, err := os.Stat(m.To); err == nil && !info.IsDir() {
			moveSidecars(m.From, m.To)
		}
		if err := s.store.SetBookFilePath(m.FileID, m.To); err != nil {
			return applied, skips, fmt.Errorf("recording move of %s: %w", m.From, err)
		}
		for _, r := range roots {
			if strings.HasPrefix(m.From, r.Path+string(filepath.Separator)) {
				sweepEmptyDirs(filepath.Dir(m.From), r.Path)
				break
			}
		}
		applied = append(applied, m)
	}
	if len(applied) > 0 {
		slog.Info("organized files", "moved", len(applied), "skipped", len(skips))
	}
	return applied, skips, nil
}

// targetPath renders the template-defined location for one file, per media
// type. For multi-file audiobooks (the record's path is the book folder) the
// target is the folder itself.
func (s *Service) targetPath(f *library.BookFile, rootByID map[int64]string) (string, string, error) {
	root, ok := rootByID[f.RootFolderID]
	if !ok {
		return "", "", fmt.Errorf("root folder %d no longer exists", f.RootFolderID)
	}
	book, err := s.store.GetBook(f.BookID)
	if err != nil {
		return "", "", fmt.Errorf("book %d: %w", f.BookID, err)
	}
	data, err := s.tokenData(book)
	if err != nil {
		return "", "", err
	}
	ns := s.cfg.NamingSettings()

	var target string
	switch f.MediaType {
	case "audiobook":
		bookDir := naming.Format(ns.AudiobookFile, data)
		dir := filepath.Join(root, naming.FormatPath(ns.AudiobookFolder, data), bookDir)
		if info, err := os.Stat(f.Path); err == nil && info.IsDir() {
			target = dir // the folder is the unit; tracks keep their names
		} else {
			target = filepath.Join(dir, bookDir+"."+f.Format)
		}
	case "manga":
		target = filepath.Join(root, naming.FormatPath(ns.MangaFolder, data),
			naming.Format(ns.MangaFile, data)+"."+f.Format)
	case "comic":
		target = filepath.Join(root, naming.FormatPath(ns.ComicFolder, data),
			naming.Format(ns.ComicFile, data)+"."+f.Format)
	case "magazine":
		target = filepath.Join(root, naming.FormatPath(ns.MagazineFolder, data),
			naming.Format(ns.MagazineFile, data)+"."+f.Format)
	default:
		target = filepath.Join(root, naming.FormatPath(ns.EbookFolder, data),
			naming.Format(ns.EbookFile, data)+"."+f.Format)
	}
	return target, book.Title, nil
}

// tokenData gathers everything the naming templates can reference for a book.
func (s *Service) tokenData(book *library.Book) (naming.TokenData, error) {
	author, err := s.store.GetAuthor(book.AuthorID)
	if err != nil {
		return naming.TokenData{}, fmt.Errorf("author %d: %w", book.AuthorID, err)
	}
	series, err := s.store.ListSeriesForBook(book.ID)
	if err != nil {
		return naming.TokenData{}, err
	}

	data := naming.TokenData{
		AuthorName:     author.Name,
		AuthorSortName: author.SortName,
		BookTitle:      book.Title,
	}
	if len(series) > 0 {
		data.SeriesTitle = series[0].Title
		data.SeriesPosition = series[0].Position
	}
	if len(book.ReleaseDate) >= 4 {
		data.ReleaseYear = book.ReleaseDate[:4]
	}
	return data, nil
}

// Placement says where an imported item belongs. For ebooks, Dir/FileName
// point at the single file; for audiobooks, Dir is the per-book folder
// (Audiobookshelf layout) and FileName names single-file books inside it.
type Placement struct {
	RootFolderID int64
	Variant      string // the chosen root's manga variant ('' otherwise)
	Dir          string
	FileName     string // includes extension
}

// PlaceFile computes where a newly imported item for book belongs: the
// first root folder of the media type plus that type's naming templates.
// For manga with both a colorized and a monochrome root, this picks
// whichever was added first — a grabbed file's variant isn't known from the
// release, so imports are variant-agnostic; the scanner is what records
// per-variant ownership once the file lands under a variant root.
func (s *Service) PlaceFile(book *library.Book, format, mediaType string) (*Placement, error) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		if root.MediaType != mediaType {
			continue
		}
		data, err := s.tokenData(book)
		if err != nil {
			return nil, err
		}
		ns := s.cfg.NamingSettings()
		p := &Placement{RootFolderID: root.ID, Variant: root.Variant}
		switch mediaType {
		case "audiobook":
			bookDir := naming.Format(ns.AudiobookFile, data)
			p.Dir = filepath.Join(root.Path, naming.FormatPath(ns.AudiobookFolder, data), bookDir)
			p.FileName = bookDir + "." + format
		case "manga":
			p.Dir = filepath.Join(root.Path, naming.FormatPath(ns.MangaFolder, data))
			p.FileName = naming.Format(ns.MangaFile, data) + "." + format
		case "comic":
			p.Dir = filepath.Join(root.Path, naming.FormatPath(ns.ComicFolder, data))
			p.FileName = naming.Format(ns.ComicFile, data) + "." + format
		case "magazine":
			p.Dir = filepath.Join(root.Path, naming.FormatPath(ns.MagazineFolder, data))
			p.FileName = naming.Format(ns.MagazineFile, data) + "." + format
		default:
			p.Dir = filepath.Join(root.Path, naming.FormatPath(ns.EbookFolder, data))
			p.FileName = naming.Format(ns.EbookFile, data) + "." + format
		}
		return p, nil
	}
	return nil, fmt.Errorf("no %s root folder configured", mediaType)
}

// moveSidecars relocates OPF sidecars belonging to a moved file: the
// base-named <file>.opf always follows; a metadata.opf follows only when
// the old directory holds no other media afterwards (per-book folders —
// shared author folders must never lose another book's sidecar).
func moveSidecars(from, to string) {
	oldBase := strings.TrimSuffix(from, filepath.Ext(from)) + ".opf"
	if _, err := os.Stat(oldBase); err == nil {
		newBase := strings.TrimSuffix(to, filepath.Ext(to)) + ".opf"
		os.Remove(newBase)
		os.Rename(oldBase, newBase)
	}
	oldMeta := filepath.Join(filepath.Dir(from), "metadata.opf")
	if _, err := os.Stat(oldMeta); err == nil && dirHasNoMedia(filepath.Dir(from)) {
		newMeta := filepath.Join(filepath.Dir(to), "metadata.opf")
		os.Remove(newMeta)
		os.Rename(oldMeta, newMeta)
	}
}

// dirHasNoMedia reports whether dir contains only sidecars (no files other
// than .opf, no subdirectories with content we might care about).
func dirHasNoMedia(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".opf") {
			return false
		}
	}
	return true
}

// sameFile compares paths the way the target filesystem does (Windows and
// macOS are usually case-insensitive).
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// sweepEmptyDirs removes dir and its now-empty parents, stopping at (and
// never removing) the root folder. os.Remove fails on non-empty directories,
// which is the other loop exit — not a problem to report.
func sweepEmptyDirs(dir, root string) {
	root = filepath.Clean(root)
	for {
		dir = filepath.Clean(dir)
		if dir == root || !strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
