package musiclibrary

import (
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

// testMusicRoot inserts a media_type='music' root folder directly (the
// API layer normally does this) and returns its id, for tests that need
// a root_folder_id to satisfy track_files' foreign key.
func testMusicRoot(t *testing.T, s *Store) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, t.TempDir())
	if err != nil {
		t.Fatalf("insert test root folder: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}
