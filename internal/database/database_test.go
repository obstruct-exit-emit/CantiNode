package database

import "testing"

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesMigrations(t *testing.T) {
	db := openTestDB(t)

	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if version != 2 {
		t.Errorf("latest migration version = %d, want 2", version)
	}

	for _, table := range []string{"root_folders", "artists", "albums", "tracks", "track_files", "monitored_artists", "wanted_albums", "downloads"} {
		if _, err := db.Exec("SELECT * FROM " + table + " LIMIT 0"); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	// A second Open against the same on-disk file should not re-apply
	// migrations or error.
	dsn := "file:" + t.TempDir() + "/test.db"
	db1, err := Open(dsn)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(dsn)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 2 {
		t.Errorf("schema_migrations row count = %d, want 2", count)
	}
}
