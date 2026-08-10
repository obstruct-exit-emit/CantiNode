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
	path := filepath.Join(t.TempDir(), "cantinode.db")

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

// seedThroughV009 writes a database that looks like an older CantiNode build
// left it: migrations applied only through 009 (media_type columns exist, but
// before the 011+ backfills, and long before 019 removed ebook/comic
// support), then a handful of representative rows — both prose/comic data
// (which the upgrade must remove) and a music root folder (which it must
// keep untouched). Closing the handle before Open() re-runs the real
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

	const through = "migrations/009_comics.sql"
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
	// Three root folders: ebook and comic must be dropped by the upgrade;
	// music must survive untouched.
	exec(`INSERT INTO root_folders (id, media_type, path) VALUES
		(1, 'ebook', '/lib/ebooks'), (2, 'comic', '/lib/comics'), (3, 'music', '/lib/music')`)
	// A custom profile per surviving/dropped media type — both the built-in
	// ebook default and the custom comic one must be gone after the upgrade;
	// the custom music one must survive.
	exec(`INSERT INTO quality_profiles (name, media_type, formats) VALUES ('Custom Comic', 'comic', 'cbz,cbr')`)
	exec(`INSERT INTO quality_profiles (name, media_type, formats) VALUES ('Custom Music', 'music', 'flac,mp3')`)
	// An author with a prose book and a comic volume — all dropped.
	exec(`INSERT INTO authors (id, foreign_id, name, sort_name, monitored) VALUES (1, 'hc-a1', 'Test Author', 'Author, Test', 1)`)
	exec(`INSERT INTO books (id, author_id, foreign_id, title, sort_title, media_type, monitored) VALUES (1, 1, 'hc-b1', 'A Prose Book', 'prose book a', 'book', 1)`)
	exec(`INSERT INTO books (id, author_id, foreign_id, title, sort_title, media_type, monitored) VALUES (2, 1, 'hc-b2', 'Comic Vol 1', 'comic vol 1', 'comic', 1)`)
	// An owned ebook file for the prose book — dropped along with book_files.
	exec(`INSERT INTO book_files (root_folder_id, book_id, path, media_type, format) VALUES (1, 1, '/lib/ebooks/a.epub', 'ebook', 'epub')`)
	// A grab against that prose book — grabs itself survives (rebuilt), the
	// dangling book_id just becomes an inert plain integer.
	exec(`INSERT INTO grabs (book_id, title, protocol, media_type) VALUES (1, 'A Prose Book', 'usenet', 'ebook')`)
}

// TestMigrationChainDropsEbookComicKeepsMusic is the real upgrade drill: seed
// an old-schema fixture with both prose/comic data and music data, run every
// remaining migration (through 019, which removes ebook/comic support, and
// 020, the LibriNode->CantiNode rebrand) against it, and assert prose/comic
// data is gone while music data and generic rows (grabs,
// download-client-agnostic tables) survive intact.
func TestMigrationChainDropsEbookComicKeepsMusic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cantinode.db")
	seedThroughV009(t, path)

	db, err := Open(path) // applies 011..latest over the fixture
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

	// Only the music root folder survives.
	var folders []struct {
		MediaType string
		Path      string
	}
	rows, err := db.Query(`SELECT media_type, path FROM root_folders ORDER BY id`)
	if err != nil {
		t.Fatalf("query root_folders: %v", err)
	}
	for rows.Next() {
		var f struct {
			MediaType string
			Path      string
		}
		if err := rows.Scan(&f.MediaType, &f.Path); err != nil {
			t.Fatalf("scan root_folder: %v", err)
		}
		folders = append(folders, f)
	}
	rows.Close()
	if len(folders) != 1 || folders[0].MediaType != "music" || folders[0].Path != "/lib/music" {
		t.Errorf("root_folders after upgrade = %+v, want just the music folder", folders)
	}

	// The ebook default and the custom comic profile are gone; the custom
	// music profile survives, and a "Standard Music" default was seeded
	// alongside it (no default existed among the surviving music profiles).
	for name, wantExists := range map[string]bool{
		"Standard Ebook": false,
		"Custom Comic":   false,
		"Custom Music":   true,
		"Standard Music": true,
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM quality_profiles WHERE name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("count profile %q: %v", name, err)
		}
		exists := n == 1
		if exists != wantExists {
			t.Errorf("quality profile %q exists = %v, want %v", name, exists, wantExists)
		}
	}
	var defaults int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quality_profiles WHERE is_default = 1`).Scan(&defaults); err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	if defaults != 1 {
		t.Errorf("default profile count = %d, want exactly 1", defaults)
	}

	// The whole prose/comic domain is gone.
	for _, table := range []string{"authors", "books", "series", "series_books", "editions", "book_files"} {
		if _, err := db.Query(`SELECT 1 FROM ` + table); err == nil {
			t.Errorf("table %q should have been dropped by the upgrade", table)
		}
	}

	// grabs survives the rebuild — its dangling book_id (the deleted prose
	// book) is now just an inert integer, not a broken foreign key.
	var grabTitle string
	var grabBookID int64
	if err := db.QueryRow(`SELECT title, book_id FROM grabs WHERE title = 'A Prose Book'`).
		Scan(&grabTitle, &grabBookID); err != nil {
		t.Fatalf("grabs row did not survive the upgrade: %v", err)
	}
	if grabBookID != 1 {
		t.Errorf("grab book_id = %d, want 1 (preserved as a plain value)", grabBookID)
	}
}
