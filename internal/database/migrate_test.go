package database

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// allMigrations returns the embedded migration names in applied order.
func allMigrations(t *testing.T) []string {
	t.Helper()
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	sort.Strings(names)
	return names
}

// TestFreshDatabaseAppliesEveryMigration is the floor: a brand-new file must
// apply the whole chain with nothing recorded twice, and re-opening it must be
// a clean no-op (idempotent). If a new migration is malformed or the recording
// logic regresses, this fails first.
func TestFreshDatabaseAppliesEveryMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "librinode.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&got); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := len(allMigrations(t)); got != want {
		t.Errorf("recorded %d migrations, want %d", got, want)
	}
	db.Close()

	// Re-open: migrate() should see everything applied and change nothing.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer db2.Close()
	var got2 int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&got2); err != nil {
		t.Fatalf("count after re-open: %v", err)
	}
	if got2 != got {
		t.Errorf("re-open changed migration count: %d -> %d", got, got2)
	}
}

// seedThroughV009 writes a database that looks like an older LibriNode build
// left it: migrations applied only through 009 (media_type columns exist, but
// before the 010 table rebuilds and the 012/013/014 backfills), then a handful
// of representative rows. Closing the handle before Open() re-runs the real
// migration chain over this fixture — the whole point of the upgrade drill.
func seedThroughV009(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(OFF)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const through = "migrations/009_manga_comics.sql"
	for _, name := range allMigrations(t) {
		if name > through {
			break
		}
		script, err := migrationsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}

	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	// Two root folders (the manga one exercises the 014 variant backfill).
	exec(`INSERT INTO root_folders (id, media_type, path) VALUES (1, 'ebook', '/lib/ebooks'), (2, 'manga', '/lib/manga')`)
	// A custom profile alongside the built-in default — both must survive the
	// 010 quality_profiles rebuild.
	exec(`INSERT INTO quality_profiles (name, media_type, formats) VALUES ('Custom Manga', 'manga', 'cbz,cbr')`)
	// An author with a prose book and a manga volume.
	exec(`INSERT INTO authors (id, foreign_id, name, sort_name, monitored) VALUES (1, 'hc-a1', 'Test Author', 'Author, Test', 1)`)
	exec(`INSERT INTO books (id, author_id, foreign_id, title, sort_title, media_type, monitored) VALUES (1, 1, 'hc-b1', 'A Prose Book', 'prose book a', 'book', 1)`)
	exec(`INSERT INTO books (id, author_id, foreign_id, title, sort_title, media_type, monitored) VALUES (2, 1, 'hc-b2', 'Manga Vol 1', 'manga vol 1', 'manga', 1)`)
	// An owned audiobook file for the prose book — the 012 backfill should read
	// this and flip the book (and, via 013, the author) into the audiobook library.
	exec(`INSERT INTO book_files (root_folder_id, book_id, path, media_type, format) VALUES (1, 1, '/lib/ebooks/a.m4b', 'audiobook', 'm4b')`)
}

// TestMigrationChainPreservesOldData is the real upgrade drill: seed an
// old-schema fixture, run every remaining migration against it, and assert the
// data comes through whole — table rebuilds keep their rows, and the backfills
// compute the values an upgrading user would expect. A migration that drops a
// column, filters rows on rebuild, or botches a backfill fails here rather than
// silently eating a real library's data.
func TestMigrationChainPreservesOldData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "librinode.db")
	seedThroughV009(t, path)

	db, err := Open(path) // applies 010..latest over the fixture
	if err != nil {
		t.Fatalf("Open (migrate fixture to head): %v", err)
	}
	defer db.Close()

	// Whole chain recorded.
	var migCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := len(allMigrations(t)); migCount != want {
		t.Errorf("after upgrade: %d migrations recorded, want %d", migCount, want)
	}

	// 010 rebuilt root_folders (create-copy-drop-rename); both rows must survive.
	var rootCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM root_folders`).Scan(&rootCount); err != nil {
		t.Fatalf("count root_folders: %v", err)
	}
	if rootCount != 2 {
		t.Errorf("root_folders survived rebuild as %d rows, want 2", rootCount)
	}
	var ebookPath string
	if err := db.QueryRow(`SELECT path FROM root_folders WHERE id = 1`).Scan(&ebookPath); err != nil {
		t.Fatalf("root_folder 1: %v", err)
	}
	if ebookPath != "/lib/ebooks" {
		t.Errorf("root_folder 1 path = %q, want /lib/ebooks", ebookPath)
	}

	// 014 backfill: existing manga roots default to the monochrome variant.
	var mangaVariant string
	if err := db.QueryRow(`SELECT variant FROM root_folders WHERE id = 2`).Scan(&mangaVariant); err != nil {
		t.Fatalf("manga root variant: %v", err)
	}
	if mangaVariant != "mono" {
		t.Errorf("manga root variant = %q, want mono (014 backfill)", mangaVariant)
	}

	// 010 rebuilt quality_profiles too; both the built-in default and the
	// custom profile must remain.
	for _, name := range []string{"Standard Ebook", "Custom Manga"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM quality_profiles WHERE name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("count profile %q: %v", name, err)
		}
		if n != 1 {
			t.Errorf("quality profile %q survived rebuild as %d rows, want 1", name, n)
		}
	}

	// 012 backfill on the prose book: implicitly in the ebook library and
	// monitored there; and — because it owns an audiobook file — in the
	// audiobook library.
	var inEbook, ebookMon, inAudio int
	if err := db.QueryRow(
		`SELECT in_ebook_library, ebook_monitored, in_audiobook_library FROM books WHERE id = 1`,
	).Scan(&inEbook, &ebookMon, &inAudio); err != nil {
		t.Fatalf("book 1 membership: %v", err)
	}
	if inEbook != 1 || ebookMon != 1 {
		t.Errorf("prose book ebook membership = (in=%d mon=%d), want (1,1)", inEbook, ebookMon)
	}
	if inAudio != 1 {
		t.Errorf("prose book with an owned audiobook file: in_audiobook_library = %d, want 1", inAudio)
	}

	// The manga volume is not a prose 'book', so the 012 backfill must leave it
	// out of the prose libraries.
	var mangaInEbook int
	if err := db.QueryRow(`SELECT in_ebook_library FROM books WHERE id = 2`).Scan(&mangaInEbook); err != nil {
		t.Fatalf("manga book membership: %v", err)
	}
	if mangaInEbook != 0 {
		t.Errorf("manga volume in_ebook_library = %d, want 0", mangaInEbook)
	}

	// 013 backfill: the author inherits both memberships from the prose book.
	var authorEbook, authorAudio int
	if err := db.QueryRow(
		`SELECT in_ebook_library, in_audiobook_library FROM authors WHERE id = 1`,
	).Scan(&authorEbook, &authorAudio); err != nil {
		t.Fatalf("author membership: %v", err)
	}
	if authorEbook != 1 || authorAudio != 1 {
		t.Errorf("author membership = (ebook=%d audio=%d), want (1,1)", authorEbook, authorAudio)
	}

	// 015 added provider_override with an empty default.
	var override string
	if err := db.QueryRow(`SELECT provider_override FROM authors WHERE id = 1`).Scan(&override); err != nil {
		t.Fatalf("author provider_override: %v", err)
	}
	if override != "" {
		t.Errorf("author provider_override = %q, want empty default", override)
	}
}
