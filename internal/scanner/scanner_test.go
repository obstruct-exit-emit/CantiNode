package scanner

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/library"
)

// fx bundles everything the scanner tests need.
type fx struct {
	svc     *Service
	store   *library.Store
	db      *sql.DB
	rootDir string
}

func TestParsePath(t *testing.T) {
	cases := []struct {
		path          string
		author, title string
	}{
		{"Terry Pratchett/Mort.epub", "Terry Pratchett", "Mort"},
		// Our own naming template's output must re-match its book.
		{"Terry Pratchett/Discworld 8 - Guards! Guards!.epub", "Terry Pratchett", "Discworld 8 - Guards! Guards!"},
		{"Terry Pratchett/Discworld/01 - The Colour of Magic.epub", "Terry Pratchett", "The Colour of Magic"},
		{"Terry Pratchett/Terry Pratchett - Mort.epub", "Terry Pratchett", "Mort"},
		{"Terry Pratchett - Mort.epub", "Terry Pratchett", "Mort"},
		{"Mort.epub", "", "Mort"},
		{"Ursula K. Le Guin/1.5 - The Word for World Is Forest.pdf", "Ursula K. Le Guin", "The Word for World Is Forest"},
	}
	for _, c := range cases {
		got := ParsePath(filepath.FromSlash(c.path))
		if got.Author != c.author || got.Title != c.title {
			t.Errorf("ParsePath(%q) = %+v, want author %q title %q", c.path, got, c.author, c.title)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"The Colour of Magic": "colour of magic",
		"Mort":                "mort",
		"Ursula K. Le Guin":   "ursula k le guin",
		"Don't Panic!":        "don t panic",
		"A Hat Full of Sky":   "hat full of sky",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleKeys(t *testing.T) {
	keys := TitleKeys("Good Omens: The Nice and Accurate Prophecies")
	if len(keys) != 2 || keys[1] != "good omens" {
		t.Errorf("TitleKeys = %v", keys)
	}
}

// fixture creates a store with one root folder, two authors, three books,
// and a populated on-disk layout.
func fixture(t *testing.T) fx {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	tp := &library.Author{Source: "hardcover", ForeignID: "100", Name: "Terry Pratchett", Monitored: true}
	if err := store.UpsertAuthor(tp); err != nil {
		t.Fatal(err)
	}
	ng := &library.Author{Source: "hardcover", ForeignID: "200", Name: "Neil Gaiman", Monitored: true}
	if err := store.UpsertAuthor(ng); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*library.Book{
		{AuthorID: tp.ID, Source: "hardcover", ForeignID: "1", Title: "The Colour of Magic", Monitored: true},
		{AuthorID: tp.ID, Source: "hardcover", ForeignID: "2", Title: "Mort", Monitored: true},
		{AuthorID: ng.ID, Source: "hardcover", ForeignID: "3", Title: "Coraline", Monitored: true},
	} {
		if err := store.UpsertBook(b); err != nil {
			t.Fatal(err)
		}
	}

	rootDir := t.TempDir()
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, rootDir); err != nil {
		t.Fatal(err)
	}

	write := func(rel string) {
		path := filepath.Join(rootDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ebook-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Terry Pratchett/The Colour of Magic.epub") // author+title match
	write("Terry Pratchett/Discworld/02 - Mort.epub") // series dir + index prefix
	write("Coraline.epub")                            // title-only, unambiguous
	write("Terry Pratchett/notes.txt")                // ignored extension
	write("Unknown Author/Mystery Novel.epub")        // unmatched

	return fx{svc: New(store), store: store, db: db, rootDir: rootDir}
}

func TestScanMatchesAndReconciles(t *testing.T) {
	f := fixture(t)
	svc, store, rootDir := f.svc, f.store, f.rootDir
	ctx := context.Background()

	result, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Roots != 1 || result.Scanned != 4 || result.Matched != 3 || result.Unmatched != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	// hasFile flips on matched books only.
	books, _ := store.ListBooks(0)
	byTitle := map[string]library.Book{}
	for _, b := range books {
		byTitle[b.Title] = b
	}
	for _, title := range []string{"The Colour of Magic", "Mort", "Coraline"} {
		if !byTitle[title].HasFile {
			t.Errorf("%s should have a file", title)
		}
	}

	unmatched, _ := store.ListUnmatchedBookFiles()
	if len(unmatched) != 1 || filepath.Base(unmatched[0].Path) != "Mystery Novel.epub" {
		t.Fatalf("unmatched = %+v", unmatched)
	}

	// File details recorded.
	mortFiles, _ := store.ListBookFiles(byTitle["Mort"].ID)
	if len(mortFiles) != 1 || mortFiles[0].Format != "epub" || mortFiles[0].Size == 0 {
		t.Fatalf("mort files = %+v", mortFiles)
	}

	// Re-scan is idempotent.
	result2, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Scanned != 4 || result2.Removed != 0 {
		t.Fatalf("re-scan result = %+v", result2)
	}

	// Deleting a file on disk prunes its record on the next scan.
	if err := os.Remove(filepath.Join(rootDir, "Terry Pratchett", "Discworld", "02 - Mort.epub")); err != nil {
		t.Fatal(err)
	}
	result3, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result3.Scanned != 3 || result3.Removed != 1 {
		t.Fatalf("post-delete result = %+v", result3)
	}
	books, _ = store.ListBooks(0)
	for _, b := range books {
		if b.Title == "Mort" && b.HasFile {
			t.Error("Mort still hasFile after its file was removed")
		}
	}
}

func TestScanUnmatchedGainsMatchAfterBookAdded(t *testing.T) {
	f := fixture(t)
	svc, store := f.svc, f.store
	ctx := context.Background()

	if _, err := svc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	unmatched, _ := store.ListUnmatchedBookFiles()
	if len(unmatched) != 1 {
		t.Fatalf("expected 1 unmatched file, got %d", len(unmatched))
	}

	// The mystery book gets added to the library; a re-scan matches the file.
	author := &library.Author{Source: "hardcover", ForeignID: "300", Name: "Unknown Author", Monitored: true}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	book := &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "4", Title: "Mystery Novel", Monitored: true}
	if err := store.UpsertBook(book); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	unmatched, _ = store.ListUnmatchedBookFiles()
	if len(unmatched) != 0 {
		t.Fatalf("still unmatched after adding the book: %+v", unmatched)
	}
	got, _ := store.GetBook(book.ID)
	if !got.HasFile {
		t.Error("book should have gained its file on re-scan")
	}
}

// TestScanScopedToMediaType: Scan("ebook") walks only ebook roots, leaving
// other libraries' files untouched — the per-library Scan-files button.
func TestScanScopedToMediaType(t *testing.T) {
	f := fixture(t) // one ebook root with matchable files
	// Add a manga root with a stray file that a full scan would record.
	mangaRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('manga', ?)`, mangaRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mangaRoot, "Some Series v01.cbz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Scan only ebooks: the manga root is skipped entirely.
	res, err := f.svc.Scan(context.Background(), "ebook")
	if err != nil {
		t.Fatalf("scoped scan: %v", err)
	}
	if res.Roots != 1 {
		t.Errorf("ebook-scoped scan walked %d roots, want 1 (manga skipped)", res.Roots)
	}

	// The manga file was never recorded.
	unmatched, _ := f.store.ListUnmatchedBookFiles()
	for _, u := range unmatched {
		if filepath.Base(u.Path) == "Some Series v01.cbz" {
			t.Error("ebook-scoped scan recorded a manga file")
		}
	}

	// A full scan (no filter) does walk both roots.
	res, err = f.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}
	if res.Roots != 2 {
		t.Errorf("unscoped scan walked %d roots, want 2", res.Roots)
	}
}

// TestScanMatchesByIdentifier: a file the title parser can't place still
// matches when it (or its embedded epub metadata) names an ISBN of a known
// edition — and a file with neither a usable identifier nor a title match still
// falls through to Unmatched, unchanged.
func TestScanMatchesByIdentifier(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	author := &library.Author{Source: "hardcover", ForeignID: "1", Name: "George R. R. Martin", Monitored: true}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	// Two books, each with an ISBN-bearing edition.
	got := &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "b1", Title: "A Game of Thrones", Monitored: true}
	clash := &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "b2", Title: "A Clash of Kings", Monitored: true}
	for _, b := range []*library.Book{got, clash} {
		if err := store.UpsertBook(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertEdition(&library.Edition{BookID: got.ID, Source: "hardcover", ForeignID: "e1", ISBN13: "9780553380163", Format: "ebook"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEdition(&library.Edition{BookID: clash.ID, Source: "hardcover", ForeignID: "e2", ISBN13: "9780553381696", Format: "ebook"}); err != nil {
		t.Fatal(err)
	}

	rootDir := t.TempDir()
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, rootDir); err != nil {
		t.Fatal(err)
	}
	writeAt := func(rel string, body []byte) {
		path := filepath.Join(rootDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Filename ISBN, title unrecognizable ("got_dl_final").
	writeAt("George R. R. Martin/got_dl_final_9780553380163.epub", []byte("x"))
	// Embedded-metadata ISBN: filename names neither the title nor the ISBN.
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0"><metadata><dc:identifier>urn:isbn:9780553381696</dc:identifier></metadata></package>`
	writeAt("George R. R. Martin/scan0042.epub", buildEpub(t, opf))
	// No identifier, no title match → stays unmatched.
	writeAt("George R. R. Martin/Totally Unrelated Filename.epub", []byte("x"))

	if _, err := New(store).Scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	gotBook, _ := store.GetBook(got.ID)
	if !gotBook.HasFile {
		t.Error("A Game of Thrones should have matched by filename ISBN")
	}
	clashBook, _ := store.GetBook(clash.ID)
	if !clashBook.HasFile {
		t.Error("A Clash of Kings should have matched by embedded epub ISBN")
	}
	unmatched, _ := store.ListUnmatchedBookFiles()
	if len(unmatched) != 1 || filepath.Base(unmatched[0].Path) != "Totally Unrelated Filename.epub" {
		t.Fatalf("unmatched = %+v (want just the unrelated file)", unmatched)
	}
}

// buildEpub returns the bytes of a minimal epub carrying the given OPF.
func buildEpub(t *testing.T, opf string) []byte {
	t.Helper()
	path := writeEpub(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"content.opf":            opf,
	})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestScanAudiobookDiscSubfolders: a book folder holding only disc-style
// subfolders (CD1/CD2) is one multi-disc book unit, not a navigation level —
// and never two bogus "CD1"/"CD2" books.
func TestScanAudiobookDiscSubfolders(t *testing.T) {
	f := fixture(t)

	abRoot := t.TempDir()
	write := func(rel string, size int) {
		path := filepath.Join(abRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Terry Pratchett/Mort/CD1/01 - Opening.mp3", 100)
	write("Terry Pratchett/Mort/CD1/02 - Death.mp3", 150)
	write("Terry Pratchett/Mort/CD2/01 - Opening.mp3", 250)
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	books, _ := f.store.ListBooks(0)
	var mort library.Book
	for _, b := range books {
		if b.Title == "Mort" {
			mort = b
		}
	}
	if !mort.HasAudiobookFile {
		t.Fatal("multi-disc book not recognized as an audiobook unit")
	}
	var ab *library.BookFile
	files, _ := f.store.ListBookFiles(mort.ID)
	for i := range files {
		if files[i].MediaType == "audiobook" {
			ab = &files[i]
		}
	}
	if ab == nil {
		t.Fatal("no audiobook file recorded")
	}
	if want := filepath.Join(abRoot, "Terry Pratchett", "Mort"); ab.Path != want {
		t.Errorf("unit path = %q, want the book folder %q", ab.Path, want)
	}
	if ab.Size != 500 {
		t.Errorf("unit size = %d, want 500 (all discs summed)", ab.Size)
	}
	// The discs themselves must not surface as unmatched "books".
	unmatched, _ := f.store.ListUnmatchedBookFiles()
	for _, u := range unmatched {
		base := filepath.Base(u.Path)
		if base == "CD1" || base == "CD2" {
			t.Errorf("disc folder leaked as a unit: %s", u.Path)
		}
	}
}

// TestScanMatchesOrganizedTemplate: the scanner recognizes its own naming
// templates' output — "Author/Title (Year)/Author - Series N - Title
// (Year).epub" — so organizing never orphans a file on the next scan.
func TestScanMatchesOrganizedTemplate(t *testing.T) {
	f := fixture(t)

	root := t.TempDir()
	path := filepath.Join(root, "Terry Pratchett", "Mort (1987)",
		"Terry Pratchett - Discworld 4 - Mort (1987).epub")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, root); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	books, _ := f.store.ListBooks(0)
	for _, b := range books {
		if b.Title == "Mort" {
			if !b.HasEbookFile {
				t.Fatal("organized template filename did not match its book")
			}
			return
		}
	}
	t.Fatal("Mort not found")
}

// TestScanPrefersFullTitleOverSubtitleVariant: "Mort" and "Mort: The
// Illustrated Screenplay" both emit the key "mort" (the latter via its
// subtitle cut). A file named plain "Mort" must match the real book, not
// whichever derivative work was indexed last.
func TestScanPrefersFullTitleOverSubtitleVariant(t *testing.T) {
	f := fixture(t)

	books, _ := f.store.ListBooks(0)
	var mort library.Book
	for _, b := range books {
		if b.Title == "Mort" {
			mort = b
		}
	}
	// Indexed after the real book — under last-wins it would steal the key.
	deriv := &library.Book{AuthorID: mort.AuthorID, Source: "hardcover", ForeignID: "901",
		Title: "Mort: The Illustrated Screenplay", Monitored: true}
	if err := f.store.UpsertBook(deriv); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	path := filepath.Join(root, "Terry Pratchett", "Mort (1987)",
		"Terry Pratchett - Mort (1987).epub")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, root); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	if files, _ := f.store.ListBookFiles(mort.ID); len(files) == 0 {
		derivFiles, _ := f.store.ListBookFiles(deriv.ID)
		t.Fatalf("file went to the wrong book: real Mort has none, derivative has %d", len(derivFiles))
	}
}

// TestScanKeepsManualMatch: a manually imported file whose name the scanner
// can't match on its own survives a rescan — scans only add matches, never
// silently clear them.
func TestScanKeepsManualMatch(t *testing.T) {
	f := fixture(t)

	root := t.TempDir()
	path := filepath.Join(root, "totally-cryptic-name.epub")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, root); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	unmatched, _ := f.store.ListUnmatchedBookFiles()
	var fileID int64
	for _, u := range unmatched {
		if u.Path == path {
			fileID = u.ID
		}
	}
	if fileID == 0 {
		t.Fatal("cryptic file should start unmatched")
	}

	// Manual import (what the existing-file flow does), then rescan.
	var mort int64
	books, _ := f.store.ListBooks(0)
	for _, b := range books {
		if b.Title == "Mort" {
			mort = b.ID
		}
	}
	if err := f.store.SetBookFileBook(fileID, mort); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	files, _ := f.store.ListBookFiles(mort)
	found := false
	for _, bf := range files {
		if bf.Path == path {
			found = true
		}
	}
	if !found {
		t.Fatal("rescan cleared the manual match")
	}
}

func TestScanAudiobookRoot(t *testing.T) {
	f := fixture(t)

	// Audiobook root: one multi-file book dir, one single-file book, one
	// unmatched dir, junk that must be ignored.
	abRoot := t.TempDir()
	write := func(rel string, size int) {
		path := filepath.Join(abRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Terry Pratchett/Mort/Part 01.mp3", 100)
	write("Terry Pratchett/Mort/Part 02.mp3", 200)
	write("Terry Pratchett/Mort/cover.jpg", 10) // non-audio, excluded from size
	write("Terry Pratchett/The Colour of Magic.m4b", 500)
	write("Unknown Reader/Mystery Tape/track.mp3", 50)
	write("notes.txt", 5)
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Ebook root: 4 scanned (fixture). Audiobook root: 3 units.
	if result.Roots != 2 || result.Scanned != 7 {
		t.Fatalf("result = %+v", result)
	}

	// Multi-file book: dir path, summed audio size, matched to Mort.
	books, _ := f.store.ListBooks(0)
	var mort, tcom library.Book
	for _, b := range books {
		switch b.Title {
		case "Mort":
			mort = b
		case "The Colour of Magic":
			tcom = b
		}
	}
	if !mort.HasAudiobookFile || !tcom.HasAudiobookFile {
		t.Fatalf("audiobook flags: mort=%v tcom=%v", mort.HasAudiobookFile, tcom.HasAudiobookFile)
	}
	mortFiles, _ := f.store.ListBookFiles(mort.ID)
	var ab *library.BookFile
	for i := range mortFiles {
		if mortFiles[i].MediaType == "audiobook" {
			ab = &mortFiles[i]
		}
	}
	if ab == nil {
		t.Fatal("no audiobook file recorded for Mort")
	}
	if ab.Path != filepath.Join(abRoot, "Terry Pratchett", "Mort") {
		t.Errorf("unit path = %q", ab.Path)
	}
	if ab.Size != 300 || ab.Format != "mp3" {
		t.Errorf("unit = size %d format %s, want 300 mp3", ab.Size, ab.Format)
	}

	// Ebook ownership is independent: Mort's ebook flag comes from the ebook
	// root fixture, and having an audiobook must not fake ebook ownership.
	if !mort.HasEbookFile {
		t.Error("mort should still have its ebook file")
	}
	unmatched, _ := f.store.ListUnmatchedBookFiles()
	foundMystery := false
	for _, u := range unmatched {
		if filepath.Base(u.Path) == "Mystery Tape" && u.MediaType == "audiobook" {
			foundMystery = true
		}
	}
	if !foundMystery {
		t.Errorf("mystery tape not in unmatched: %+v", unmatched)
	}

	// Re-scan is idempotent; deleting the book dir prunes the record.
	if err := os.RemoveAll(filepath.Join(abRoot, "Terry Pratchett", "Mort")); err != nil {
		t.Fatal(err)
	}
	result, err = f.svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 {
		t.Fatalf("post-delete result = %+v", result)
	}
}

func TestScanComicRoot(t *testing.T) {
	f := fixture(t)

	// Manga series with two volumes in the library.
	series := &library.Series{Source: "anilist", ForeignID: "500", Title: "Berserk",
		MediaType: "manga", Monitored: true}
	if err := f.store.UpsertSeries(series); err != nil {
		t.Fatal(err)
	}
	author := &library.Author{Source: "anilist", ForeignID: "creator:miura", Name: "Kentarou Miura"}
	if err := f.store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		vol := &library.Book{AuthorID: author.ID, Source: "anilist", MediaType: "manga",
			ForeignID: filepath.Join("500-v", string(rune('0'+i))), Title: "Berserk Vol. " + string(rune('0'+i)), Monitored: true}
		if err := f.store.UpsertBook(vol); err != nil {
			t.Fatal(err)
		}
		if err := f.store.LinkBookSeries(vol.ID, series.ID, float64(i)); err != nil {
			t.Fatal(err)
		}
	}

	mangaRoot := t.TempDir()
	write := func(rel string) {
		path := filepath.Join(mangaRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("pages"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Berserk/Berserk v01.cbz")     // dir-named series
	write("Berserk v02 (Digital).cbz")   // loose, series from filename
	write("One Piece/One Piece v01.cbz") // unknown series → unmatched
	write("Berserk/notes.txt")           // ignored
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('manga', ?)`, mangaRoot); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Ebook fixture root scans 4; manga root scans 3 archives, 2 matched.
	if result.Scanned != 7 || result.Unmatched != 2 {
		t.Fatalf("result = %+v", result)
	}

	volumes, _ := f.store.ListVolumes(series.ID)
	if len(volumes) != 2 || !volumes[0].HasFile || !volumes[1].HasFile {
		t.Fatalf("volumes = %+v", volumes)
	}
	files, _ := f.store.ListBookFiles(volumes[0].ID)
	if len(files) != 1 || files[0].MediaType != "manga" || files[0].Format != "cbz" {
		t.Fatalf("files = %+v", files)
	}
}

// TestScanMangaVariants: colorized and monochrome manga live in separate
// root folders but ONE library; a volume tracks each variant's ownership
// independently, sharing the single volume metadata row.
func TestScanMangaVariants(t *testing.T) {
	f := fixture(t)

	series := &library.Series{Source: "anilist", ForeignID: "600", Title: "Berserk",
		MediaType: "manga", Monitored: true}
	if err := f.store.UpsertSeries(series); err != nil {
		t.Fatal(err)
	}
	author := &library.Author{Source: "anilist", ForeignID: "creator:m", Name: "Miura"}
	if err := f.store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		vol := &library.Book{AuthorID: author.ID, Source: "anilist", MediaType: "manga",
			ForeignID: "600-v" + string(rune('0'+i)), Title: "Berserk Vol. " + string(rune('0'+i)), Monitored: true}
		if err := f.store.UpsertBook(vol); err != nil {
			t.Fatal(err)
		}
		if err := f.store.LinkBookSeries(vol.ID, series.ID, float64(i)); err != nil {
			t.Fatal(err)
		}
	}

	writeInto := func(dir, rel string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("pages"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Colorized root has vol 1 only; monochrome root has both volumes.
	colorRoot := t.TempDir()
	monoRoot := t.TempDir()
	writeInto(colorRoot, "Berserk/Berserk v01.cbz")
	writeInto(monoRoot, "Berserk/Berserk v01.cbz")
	writeInto(monoRoot, "Berserk/Berserk v02.cbz")
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, variant, path) VALUES ('manga', 'color', ?)`, colorRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, variant, path) VALUES ('manga', 'mono', ?)`, monoRoot); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	volumes, _ := f.store.ListVolumes(series.ID)
	if len(volumes) != 2 {
		t.Fatalf("volumes = %d, want 2", len(volumes))
	}
	// Vol 1: owned in both variants. Vol 2: monochrome only.
	v1, v2 := volumes[0], volumes[1]
	if !v1.HasColorFile || !v1.HasMonoFile {
		t.Errorf("vol 1 variants: color=%v mono=%v, want both owned", v1.HasColorFile, v1.HasMonoFile)
	}
	if v2.HasColorFile || !v2.HasMonoFile {
		t.Errorf("vol 2 variants: color=%v mono=%v, want mono only", v2.HasColorFile, v2.HasMonoFile)
	}
	// The variants share ONE volume row — vol 1 has two files, one per variant.
	files, _ := f.store.ListBookFiles(v1.ID)
	if len(files) != 2 {
		t.Fatalf("vol 1 files = %d, want 2 (one per variant)", len(files))
	}
	gotVariants := map[string]bool{}
	for _, bf := range files {
		gotVariants[bf.Variant] = true
	}
	if !gotVariants["color"] || !gotVariants["mono"] {
		t.Errorf("vol 1 file variants = %v, want color+mono", gotVariants)
	}
}

func TestScanMatchesOwnTemplateOutput(t *testing.T) {
	// Files organized by the default naming template
	// ("{Series Title} {Series Position} - {Book Title}") must re-match
	// their book on subsequent scans.
	f := fixture(t)

	guards := &library.Book{AuthorID: 1, Source: "hardcover", ForeignID: "g8",
		Title: "Guards! Guards!", Monitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.rootDir, "Terry Pratchett", "Discworld 8 - Guards! Guards!.epub")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetBook(guards.ID)
	if !got.HasEbookFile {
		t.Fatal("template-named file did not re-match its book")
	}
}

func TestIssueIdentifier(t *testing.T) {
	cases := map[string]string{
		"The Economist - 2026-07-04.pdf":    "2026-07-04",
		"The Economist 2026.07.04 (retail)": "2026-07-04",
		"National Geographic - July 2026":   "2026-07",
		"Wired - Sept 2025 Retail EPUB":     "2025-09",
		"Retro Gamer Issue 261 pdf":         "issue-261",
		"MagPi No. 143":                     "issue-143",
		"Some Book Without A Date":          "",
		"The Economist - January 15, 2026":  "2026-01-15",
	}
	for in, want := range cases {
		if got := IssueIdentifier(in); got != want {
			t.Errorf("IssueIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanMagazineRoot(t *testing.T) {
	f := fixture(t)

	// A monitored magazine in the library, no issues yet.
	mag := &library.Series{Source: "manual", ForeignID: "magazine:economist",
		Title: "The Economist", MediaType: "magazine", Monitored: true, MonitorNew: true}
	if err := f.store.UpsertSeries(mag); err != nil {
		t.Fatal(err)
	}

	magRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('magazine', ?)`, magRoot); err != nil {
		t.Fatal(err)
	}
	write := func(rel string) {
		path := filepath.Join(magRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("The Economist/The Economist - 2026-07-04.pdf") // known magazine → issue created
	write("The Economist/The Economist - 2026-06-27.pdf") // second issue
	write("Unknown Weekly/Unknown Weekly - 2026-01.pdf")  // no series → unmatched

	result, err := f.svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched < 2 || result.Unmatched < 1 {
		t.Fatalf("result = %+v", result)
	}

	// Issues were materialized, owned (file attached), and unmonitored.
	volumes, err := f.store.ListVolumes(mag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 2 {
		t.Fatalf("volumes = %+v", volumes)
	}
	for _, v := range volumes {
		if !v.HasFile || v.Monitored || v.MediaType != "magazine" {
			t.Errorf("issue = title %q hasFile %v monitored %v", v.Title, v.HasFile, v.Monitored)
		}
	}

	// Re-scan is idempotent — no duplicate issues.
	if _, err := f.svc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	volumes, _ = f.store.ListVolumes(mag.ID)
	if len(volumes) != 2 {
		t.Fatalf("re-scan duplicated issues: %+v", volumes)
	}
}

func TestVolumeFromName(t *testing.T) {
	cases := map[string]float64{
		"Berserk v05.cbz":            5,
		"Berserk Vol. 12.cbz":        12,
		"Berserk Volume 3.cbz":       3,
		"The Walking Dead #112.cbr":  112,
		"Berserk v5.5.cbz":           5.5,
		"Berserk Deluxe Edition.cbz": 0,
	}
	for in, want := range cases {
		if got := VolumeFromName(in); got != want {
			t.Errorf("VolumeFromName(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScanSkipsMissingRoot(t *testing.T) {
	f := fixture(t)

	// A root folder whose directory vanished after being added.
	gone := filepath.Join(t.TempDir(), "gone")
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, gone); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan should not fail outright: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 root error, got %+v", result.Errors)
	}
	if result.Scanned != 4 {
		t.Errorf("healthy root not scanned: %+v", result)
	}
}
