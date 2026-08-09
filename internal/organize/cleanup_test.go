package organize

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/library"
)

// cleanupFixture builds an ebook root with: a tracked epub, an untracked epub
// (wanted media), a sidecar + cover (kept), junk (.nfo, .torrent), an .mp3
// (wrong library), and an empty directory chain.
func cleanupFixture(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)
	cfg, err := config.Load(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "ebooks")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var rootID int64
	if err := db.QueryRow(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?) RETURNING id`, root).Scan(&rootID); err != nil {
		t.Fatal(err)
	}

	write := func(rel string) string {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tracked := write("Author/Book.epub")
	if _, err := db.Exec(`INSERT INTO book_files (root_folder_id, path, media_type) VALUES (?, ?, 'ebook')`, rootID, tracked); err != nil {
		t.Fatal(err)
	}
	write("Author/Stray.epub")     // untracked but wanted media — kept
	write("Author/Book.opf")       // sidecar — kept
	write("Author/cover.jpg")      // artwork — kept
	write("Author/release.nfo")    // junk — deleted
	write("Author/source.torrent") // junk — deleted
	write("Author/audio-dump.mp3") // wrong library's media — deleted
	if err := os.MkdirAll(filepath.Join(root, "Empty", "Deeper"), 0o755); err != nil {
		t.Fatal(err)
	}

	return New(store, cfg), root
}

func TestPlanCleanupFindsOnlyUnwanted(t *testing.T) {
	svc, _ := cleanupFixture(t)

	cleanups, skips, err := svc.PlanCleanup("ebook")
	if err != nil {
		t.Fatalf("PlanCleanup: %v", err)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v", skips)
	}
	got := map[string]bool{}
	for _, c := range cleanups {
		got[filepath.Base(c.Path)] = true
	}
	for _, want := range []string{"release.nfo", "source.torrent", "audio-dump.mp3"} {
		if !got[want] {
			t.Errorf("cleanup should include %s: %v", want, cleanups)
		}
	}
	for _, keep := range []string{"Book.epub", "Stray.epub", "Book.opf", "cover.jpg"} {
		if got[keep] {
			t.Errorf("cleanup must NOT include %s", keep)
		}
	}
}

func TestApplyCleanupDeletesAndPrunes(t *testing.T) {
	svc, root := cleanupFixture(t)

	cleanups, _, err := svc.PlanCleanup("ebook")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, c := range cleanups {
		paths = append(paths, c.Path)
	}
	// A path outside the library's roots must be refused, not deleted.
	outside := filepath.Join(t.TempDir(), "victim.nfo")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, outside)
	// A wanted path smuggled into the list must be refused too.
	paths = append(paths, filepath.Join(root, "Author", "Book.epub"))

	deleted, pruned, skips, err := svc.ApplyCleanup("ebook", paths)
	if err != nil {
		t.Fatalf("ApplyCleanup: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (nfo, torrent, mp3)", deleted)
	}
	if pruned < 2 {
		t.Errorf("pruned = %d, want >= 2 (Empty/Deeper chain)", pruned)
	}
	if len(skips) != 2 {
		t.Errorf("skips = %v, want the outside path and the wanted epub refused", skips)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("file outside the root was deleted — root confinement failed")
	}
	if _, err := os.Stat(filepath.Join(root, "Author", "Book.epub")); err != nil {
		t.Error("tracked epub was deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "Empty")); !os.IsNotExist(err) {
		t.Error("empty directory chain should have been pruned")
	}
	if _, err := os.Stat(root); err != nil {
		t.Error("the root folder itself must never be removed")
	}
}
