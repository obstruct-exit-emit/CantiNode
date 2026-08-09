package organize

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/library"
)

type fx struct {
	svc     *Service
	store   *library.Store
	db      *sql.DB
	rootDir string
	rootID  int64
	tcom    *library.Book // Discworld #1, has a file in the wrong place
	mort    *library.Book // no series, file already in place
}

func fixture(t *testing.T) fx {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	author := &library.Author{Source: "hardcover", ForeignID: "100", Name: "Terry Pratchett", Monitored: true}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	tcom := &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "1",
		Title: "The Colour of Magic", ReleaseDate: "1983-11-24", Monitored: true}
	mort := &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "2", Title: "Mort", Monitored: true}
	for _, b := range []*library.Book{tcom, mort} {
		if err := store.UpsertBook(b); err != nil {
			t.Fatal(err)
		}
	}
	series := &library.Series{Source: "hardcover", ForeignID: "7", Title: "Discworld"}
	if err := store.UpsertSeries(series); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkBookSeries(tcom.ID, series.ID, 1); err != nil {
		t.Fatal(err)
	}

	rootDir := t.TempDir()
	res, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, rootDir)
	if err != nil {
		t.Fatal(err)
	}
	rootID, _ := res.LastInsertId()

	// tcom's file is misplaced; mort's is already where the template puts it
	// (dateless book: the "(Year)" drops and the book folder is just "Mort").
	mortPath := filepath.Join(rootDir, "Terry Pratchett", "Mort", "Terry Pratchett - Mort.epub")
	writeFile(t, filepath.Join(rootDir, "downloads", "tcom_v2.FINAL.epub"))
	writeFile(t, mortPath)
	for _, f := range []*library.BookFile{
		{RootFolderID: rootID, BookID: tcom.ID, Path: filepath.Join(rootDir, "downloads", "tcom_v2.FINAL.epub"), Format: "epub"},
		{RootFolderID: rootID, BookID: mort.ID, Path: mortPath, Format: "epub"},
	} {
		if err := store.UpsertBookFile(f); err != nil {
			t.Fatal(err)
		}
	}

	return fx{svc: New(store, cfg), store: store, db: db, rootDir: rootDir, rootID: rootID, tcom: tcom, mort: mort}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPlanNonEbookTypes: the rename engine covers every media type — comic
// files move to Series templates, multi-file audiobooks move as folders (and
// carry their sidecars), single-file audiobooks land in per-book folders.
func TestPlanNonEbookTypes(t *testing.T) {
	f := fixture(t)

	// Comic volume linked to a series.
	comicRoot := t.TempDir()
	res, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('comic', ?)`, comicRoot)
	if err != nil {
		t.Fatal(err)
	}
	comicRootID, _ := res.LastInsertId()
	cSeries := &library.Series{Source: "comicvine", ForeignID: "77", Title: "The Walking Dead", MediaType: "comic"}
	if err := f.store.UpsertSeries(cSeries); err != nil {
		t.Fatal(err)
	}
	issue := &library.Book{AuthorID: f.tcom.AuthorID, Source: "comicvine", ForeignID: "77-1",
		MediaType: "comic", Title: "The Walking Dead #1", Monitored: true}
	if err := f.store.UpsertBook(issue); err != nil {
		t.Fatal(err)
	}
	if err := f.store.LinkBookSeries(issue.ID, cSeries.ID, 1); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(comicRoot, "dump", "twd001.cbz"))
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: comicRootID, BookID: issue.ID, MediaType: "comic",
		Path: filepath.Join(comicRoot, "dump", "twd001.cbz"), Format: "cbz",
	}); err != nil {
		t.Fatal(err)
	}

	// Multi-file audiobook: the record's path is the book folder.
	abRoot := t.TempDir()
	res, err = f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot)
	if err != nil {
		t.Fatal(err)
	}
	abRootID, _ := res.LastInsertId()
	abDir := filepath.Join(abRoot, "incoming", "mort audio")
	writeFile(t, filepath.Join(abDir, "01.mp3"))
	writeFile(t, filepath.Join(abDir, "metadata.opf"))
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: abRootID, BookID: f.mort.ID, MediaType: "audiobook",
		Path: abDir, Format: "mp3",
	}); err != nil {
		t.Fatal(err)
	}

	moves, skips, err := f.svc.Plan(0)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips = %v", skips)
	}
	targets := map[string]string{}
	for _, m := range moves {
		targets[m.From] = m.To
	}
	wantComic := filepath.Join(comicRoot, "The Walking Dead", "The Walking Dead #01.cbz")
	if got := targets[filepath.Join(comicRoot, "dump", "twd001.cbz")]; got != wantComic {
		t.Errorf("comic target = %q, want %q", got, wantComic)
	}
	wantAB := filepath.Join(abRoot, "Terry Pratchett", "Mort")
	if got := targets[abDir]; got != wantAB {
		t.Errorf("audiobook folder target = %q, want %q", got, wantAB)
	}

	if _, _, err := f.svc.Apply(moves); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The folder moved with its tracks and sidecar intact.
	for _, p := range []string{
		filepath.Join(wantAB, "01.mp3"),
		filepath.Join(wantAB, "metadata.opf"),
		wantComic,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("after apply, missing %s", p)
		}
	}
}

func TestPlanAndApply(t *testing.T) {
	f := fixture(t)

	moves, skips, err := f.svc.Plan(0)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips = %v", skips)
	}
	// Only the misplaced file needs a move.
	if len(moves) != 1 {
		t.Fatalf("moves = %+v", moves)
	}
	want := filepath.Join(f.rootDir, "Terry Pratchett", "The Colour of Magic (1983)",
		"Terry Pratchett - Discworld 1 - The Colour of Magic (1983).epub")
	if moves[0].To != want {
		t.Fatalf("target = %q, want %q", moves[0].To, want)
	}

	applied, skips, err := f.svc.Apply(moves)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 || len(skips) != 0 {
		t.Fatalf("applied = %+v, skips = %v", applied, skips)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("file not at target: %v", err)
	}
	// Old dir swept, root untouched.
	if _, err := os.Stat(filepath.Join(f.rootDir, "downloads")); !os.IsNotExist(err) {
		t.Error("empty source dir not swept")
	}
	if _, err := os.Stat(f.rootDir); err != nil {
		t.Error("root folder must never be swept")
	}
	// DB records the new path.
	files, _ := f.store.ListBookFiles(f.tcom.ID)
	if len(files) != 1 || files[0].Path != want {
		t.Fatalf("db path = %+v", files)
	}

	// Second plan: nothing to do.
	moves, _, err = f.svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 0 {
		t.Fatalf("expected no moves after organize, got %+v", moves)
	}
}

func TestApplyNeverOverwrites(t *testing.T) {
	f := fixture(t)

	// Occupy the target path with an existing file.
	target := filepath.Join(f.rootDir, "Terry Pratchett", "The Colour of Magic (1983)",
		"Terry Pratchett - Discworld 1 - The Colour of Magic (1983).epub")
	writeFile(t, target)

	moves, _, err := f.svc.Plan(f.tcom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %+v", moves)
	}
	applied, skips, err := f.svc.Apply(moves)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 || len(skips) != 1 {
		t.Fatalf("applied = %+v, skips = %v", applied, skips)
	}
	// Source untouched.
	if _, err := os.Stat(moves[0].From); err != nil {
		t.Errorf("source file was disturbed: %v", err)
	}
}

func TestPlanSkipsFileWithMissingBook(t *testing.T) {
	f := fixture(t)

	// Foreign keys normally make a dangling book_id impossible; simulate DB
	// corruption to exercise the defensive skip path.
	if _, err := f.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE book_files SET book_id = 9999 WHERE book_id = ?`, f.tcom.ID); err != nil {
		t.Fatal(err)
	}
	moves, skips, err := f.svc.Plan(0)
	if err != nil {
		t.Fatalf("Plan should not fail outright: %v", err)
	}
	if len(skips) != 1 {
		t.Fatalf("skips = %v", skips)
	}
	if len(moves) != 0 {
		t.Fatalf("moves = %+v", moves)
	}
}
