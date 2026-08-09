package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/librinode/librinode/internal/config"
	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/organize"
)

type fx struct {
	svc       *Service
	store     *library.Store
	downloads *download.Service
	grabs     *download.Store
	db      *sql.DB
	rootDir string
	book    *library.Book
	history []map[string]any // mutable mock SAB history
	removed []string         // nzo ids deleted from history
	delData []string         // nzo ids deleted WITH their files (del_files=1)
	packAll bool             // the pack-import-all setting
	// post-import cleanup settings
	removeCompleted bool
	deleteFiles     bool
}

func fixture(t *testing.T) *fx {
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
	f := &fx{store: store, db: db, history: []map[string]any{}}

	// Mock SABnzbd: empty queue, mutable history, delete tracking.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch q.Get("mode") {
		case "version":
			w.Write([]byte(`{"version": "4.3.2"}`))
		case "queue":
			w.Write([]byte(`{"queue": {"slots": []}}`))
		case "history":
			if q.Get("name") == "delete" {
				f.removed = append(f.removed, q.Get("value"))
				if q.Get("del_files") == "1" {
					f.delData = append(f.delData, q.Get("value"))
				}
				w.Write([]byte(`{"status": true}`))
				return
			}
			out, _ := json.Marshal(map[string]any{"history": map[string]any{"slots": f.history}})
			w.Write(out)
		default:
			w.Write([]byte(`{"status": false, "error": "unknown mode"}`))
		}
	}))
	t.Cleanup(srv.Close)

	downloads := download.NewService(download.NewStore(db))
	f.downloads = downloads
	f.grabs = downloads.Store()
	if err := downloads.Store().Add(&download.ClientConfig{
		Name: "sab", Type: download.TypeSABnzbd, Host: srv.URL,
		APIKey: "k", Category: "librinode", Enabled: true, Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Library: one monitored, fileless book and an ebook root folder.
	author := &library.Author{Source: "hardcover", ForeignID: "100", Name: "Terry Pratchett", Monitored: true}
	if err := store.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	f.book = &library.Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "1",
		Title: "Mort", ReleaseDate: "1987-11-12", Monitored: true}
	if err := store.UpsertBook(f.book); err != nil {
		t.Fatal(err)
	}
	f.rootDir = t.TempDir()
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', ?)`, f.rootDir); err != nil {
		t.Fatal(err)
	}

	f.svc = New(store, downloads, organize.New(store, cfg), func() config.ImportSettings {
		return config.ImportSettings{
			PackImportAll:        f.packAll,
			RemoveCompleted:      f.removeCompleted,
			DeleteCompletedFiles: f.deleteFiles,
		}
	})
	return f
}

// completedDownload creates a finished download on disk and a matching
// history entry.
func (f *fx) completedDownload(t *testing.T, nzoID, title string, files ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), title)
	for _, rel := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("book-bytes-"+rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.history = append(f.history, map[string]any{
		"nzo_id": nzoID, "name": title, "status": "Completed", "storage": dir, "category": "librinode",
	})
	return dir
}

// TestSeededTorrentRemovedAfterImport: a finished torrent the client has
// stopped seeding (goal reached) is removed with its data — but only once
// its grab is imported.
func TestSeededTorrentRemovedAfterImport(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	var deleted []string
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			w.Write([]byte("Ok."))
		case strings.HasSuffix(r.URL.Path, "/torrents/info"):
			w.Write([]byte(`[{"hash":"h1","name":"Terry Pratchett - Mort EPUB","state":"pausedUP","progress":1,"content_path":"/downloads/mort","category":"librinode"}]`))
		case strings.HasSuffix(r.URL.Path, "/torrents/delete"):
			r.ParseForm()
			deleted = append(deleted, r.FormValue("hashes")+":"+r.FormValue("deleteFiles"))
			w.Write([]byte("Ok."))
		default:
			w.Write([]byte("{}"))
		}
	}))
	t.Cleanup(qbit.Close)
	if err := f.grabs.Add(&download.ClientConfig{
		Name: "qbit", Type: download.TypeQBittorrent, Host: qbit.URL,
		Category: "librinode", Enabled: true, Priority: 2,
	}); err != nil {
		t.Fatal(err)
	}

	// Grab already imported earlier → the seeded torrent gets cleaned up.
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 2,
		Title: "Terry Pratchett - Mort EPUB", Protocol: download.ProtocolTorrent,
		MediaType: "ebook",
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if err := f.grabs.ResolveGrab(grabs[0].ID, download.GrabStatusImported, "test"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != "h1:true" {
		t.Fatalf("deleted = %v, want [h1:true] (remove with data)", deleted)
	}

	// A torrent with no imported LibriNode grab is never touched.
	deleted = nil
	if err := f.grabs.ResolveGrab(grabs[0].ID, download.GrabStatusFailed, "test reset"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("foreign seeded torrent was removed: %v", deleted)
	}
}

func TestImportAudiobookGrab(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	// Audiobook root folder alongside the ebook one.
	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	// Multi-file audiobook download, tracked as an audiobook grab.
	f.completedDownload(t, "nzo_ab", "Terry Pratchett - Mort Unabridged M4B",
		"Mort - 01.mp3", "Mort - 02.mp3", "cover.jpg")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_ab",
		Title: "Terry Pratchett - Mort Unabridged M4B", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}

	// Tracks landed inside the Audiobookshelf-style book folder.
	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, name := range []string{"Mort - 01.mp3", "Mort - 02.mp3"} {
		if _, err := os.Stat(filepath.Join(bookDir, name)); err != nil {
			t.Fatalf("track missing: %v", err)
		}
	}
	// Non-audio junk excluded.
	if _, err := os.Stat(filepath.Join(bookDir, "cover.jpg")); !os.IsNotExist(err) {
		t.Error("non-audio file should not be imported")
	}

	// Recorded as an audiobook unit on the book (ebook side untouched).
	book, _ := f.store.GetBook(f.book.ID)
	if !book.HasAudiobookFile || book.HasEbookFile {
		t.Fatalf("book flags = ebook %v audio %v", book.HasEbookFile, book.HasAudiobookFile)
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].MediaType != "audiobook" || files[0].Path != bookDir {
		t.Fatalf("files = %+v", files)
	}
}

// TestImportAudiobookDiscSubfolders: a multi-disc download (CD1/CD2 with
// same-named tracks) imports with its layout preserved — flattening would
// collide the names, abort the copy, and strand the grab behind the
// half-copied folder.
func TestImportAudiobookDiscSubfolders(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_cd", "Terry Pratchett - Mort Unabridged",
		filepath.Join("CD1", "01 - Opening.mp3"),
		filepath.Join("CD1", "02 - Death.mp3"),
		filepath.Join("CD2", "01 - Opening.mp3"), // same name as CD1's first track
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_cd",
		Title: "Terry Pratchett - Mort Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}

	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, rel := range []string{
		filepath.Join("CD1", "01 - Opening.mp3"),
		filepath.Join("CD1", "02 - Death.mp3"),
		filepath.Join("CD2", "01 - Opening.mp3"),
	} {
		if _, err := os.Stat(filepath.Join(bookDir, rel)); err != nil {
			t.Errorf("track missing after import: %v", err)
		}
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Path != bookDir {
		t.Fatalf("files = %+v", files)
	}
}

// TestImportAudiobookFlattensNonDiscNesting: non-disc nesting (an "mp3s"
// wrapper folder) is flattened — the scanner only recognizes book folders
// holding files and disc subfolders — and a name collision while flattening is
// qualified with its folder instead of aborting the copy.
func TestImportAudiobookFlattensNonDiscNesting(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_flat", "Terry Pratchett - Mort Unabridged",
		filepath.Join("mp3s", "01 - Opening.mp3"),
		filepath.Join("mp3s", "02 - Death.mp3"),
		filepath.Join("extras", "01 - Opening.mp3"), // collides once flattened
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_flat",
		Title: "Terry Pratchett - Mort Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}

	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, name := range []string{
		"01 - Opening.mp3", // extras' copy flattened first (lexical walk order)
		"02 - Death.mp3",
		"mp3s - 01 - Opening.mp3", // the collision, qualified with its folder
	} {
		if _, err := os.Stat(filepath.Join(bookDir, name)); err != nil {
			t.Errorf("expected flattened track %q: %v", name, err)
		}
	}
	// No non-disc subfolders survive in the book folder.
	entries, _ := os.ReadDir(bookDir)
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("book folder should be flat, found subdir %q", e.Name())
		}
	}
}

// TestImportAdoptsExistingTarget: the import target already exists on disk but
// the library has no record of it (an earlier import wrote the file before
// recording, or it was placed by hand). The import adopts the file — records
// it and resolves the grab — instead of skipping forever.
func TestImportAdoptsExistingTarget(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	// The organized file is already in place, unrecorded.
	existing := filepath.Join(f.rootDir, "Terry Pratchett", "Mort (1987)",
		"Terry Pratchett - Mort (1987).epub")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("already-on-disk"), 0o644); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_adopt", "Terry Pratchett - Mort Retail EPUB", "Mort.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_adopt",
		Title: "Terry Pratchett - Mort Retail EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	// The existing file was adopted, not overwritten.
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "already-on-disk" {
		t.Fatalf("existing file clobbered: %q %v", data, err)
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Path != existing || files[0].Size != int64(len("already-on-disk")) {
		t.Fatalf("files = %+v", files)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusImported {
		t.Errorf("grab status = %s, want imported", grabs[0].Status)
	}
}

// TestImportSweepsOrphanedGrabs: a pending grab whose download no longer
// appears in its client is resolved as failed (not blocklisted) once past the
// grace period and re-searched; fresh grabs are left alone. The exemption for
// an unreachable client is per-client: a grab sitting in a client that failed
// to answer this pass is left alone, but that must not freeze orphan
// resolution for grabs in a different, perfectly healthy client.
func TestImportSweepsOrphanedGrabs(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	researched := make(chan int64, 1)
	f.svc.OnJunkBlocklist(func(bookID int64, mediaType string) { researched <- bookID })

	// Two pending grabs with no matching download in the client: one aged past
	// the grace period (orphaned), one fresh (still settling).
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_gone",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-gone",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_new",
		Title: "Terry Pratchett - Mort Fresh EPUB", GUID: "guid-new",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	for _, g := range grabs {
		if g.ClientItemID == "nzo_gone" {
			if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
				"2020-01-01 00:00:00", g.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	byItem := map[string]download.GrabRecord{}
	grabs, _ = f.grabs.ListGrabs("")
	for _, g := range grabs {
		byItem[g.ClientItemID] = g
	}
	if g := byItem["nzo_gone"]; g.Status != download.GrabStatusFailed ||
		!strings.Contains(g.Message, "disappeared") {
		t.Errorf("orphan grab = %s %q, want failed/disappeared", g.Status, g.Message)
	}
	if g := byItem["nzo_new"]; g.Status != download.GrabStatusGrabbed {
		t.Errorf("fresh grab = %s, want still grabbed", g.Status)
	}
	// Not blocklisted — the release may be fine.
	blocked, _ := f.grabs.BlockedKeys()
	if blocked["guid-gone"] {
		t.Error("orphaned release must not be blocklisted")
	}
	// Replacement search fired for the orphan's book.
	select {
	case id := <-researched:
		if id != f.book.ID {
			t.Errorf("research bookID = %d, want %d", id, f.book.ID)
		}
	case <-time.After(2 * time.Second):
		t.Error("replacement search was not triggered")
	}

	// A second client goes down. An orphan sitting in the still-healthy "sab"
	// client (id 1) must still be swept; one sitting in the DEAD client must
	// not be — its client simply didn't answer this pass, so it isn't a true
	// orphan yet.
	dead := &download.ClientConfig{
		Name: "dead", Type: download.TypeSABnzbd, Host: "http://127.0.0.1:1",
		Category: "librinode", Enabled: true, Priority: 9,
	}
	if err := f.grabs.Add(dead); err != nil {
		t.Fatal(err)
	}
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_gone2",
		Title: "Terry Pratchett - Mort Again EPUB", GUID: "guid-gone2",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: dead.ID, ClientItemID: "nzo_on_dead",
		Title: "Terry Pratchett - Mort Yet Again EPUB", GUID: "guid-on-dead",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ = f.grabs.ListGrabs("")
	for _, g := range grabs {
		if g.ClientItemID == "nzo_gone2" || g.ClientItemID == "nzo_on_dead" {
			if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
				"2020-01-01 00:00:00", g.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.downloads.InvalidateQueue() // the API layer does this on client changes
	if _, err := f.svc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	grabs, _ = f.grabs.ListGrabs("")
	for _, g := range grabs {
		switch g.ClientItemID {
		case "nzo_gone2":
			if g.Status != download.GrabStatusFailed {
				t.Errorf("orphan in the healthy client should still sweep: %s", g.Status)
			}
		case "nzo_on_dead":
			if g.Status != download.GrabStatusGrabbed {
				t.Errorf("grab in the down client should NOT sweep: %s", g.Status)
			}
		}
	}
}

func TestImportTrackedGrab(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	f.completedDownload(t, "nzo_1", "Terry Pratchett - Mort Retail EPUB",
		"Mort.epub", "sample/tiny.txt")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_1",
		Title: "Terry Pratchett - Mort Retail EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Imported != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}

	// File landed at the template path — its own per-book folder.
	want := filepath.Join(f.rootDir, "Terry Pratchett", "Mort (1987)",
		"Terry Pratchett - Mort (1987).epub")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("imported file missing: %v", err)
	}
	// Book owns it now.
	book, _ := f.store.GetBook(f.book.ID)
	if !book.HasFile {
		t.Error("book should have a file after import")
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Format != "epub" || files[0].Size == 0 {
		t.Fatalf("book files = %+v", files)
	}
	// Grab resolved, usenet history cleaned up.
	grabs, _ := f.grabs.ListGrabs("")
	if len(grabs) != 1 || grabs[0].Status != download.GrabStatusImported {
		t.Fatalf("grabs = %+v", grabs)
	}
	if len(f.removed) != 1 || f.removed[0] != "nzo_1" {
		t.Errorf("history cleanup = %v", f.removed)
	}

	// Second pass: nothing new, nothing re-imported.
	result, err = f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 {
		t.Errorf("re-import happened: %+v", result)
	}
}

// TestImportBlocklistsSpamDownload: a completed download whose content is an
// executable (spam masquerading as the book) is failed, blocklisted, and
// triggers a search for a replacement — not left to be re-grabbed.
func TestImportBlocklistsSpamDownload(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	researched := make(chan int64, 1)
	f.svc.OnJunkBlocklist(func(bookID int64, mediaType string) { researched <- bookID })

	spamDir := f.completedDownload(t, "nzo_spam", "Terry Pratchett - Mort Retail EPUB",
		"Mort - Retail.exe")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_spam",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-spam",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusFailed {
		t.Errorf("grab status = %s, want failed", grabs[0].Status)
	}
	if !strings.Contains(grabs[0].Message, "spam") {
		t.Errorf("grab message = %q, want a spam reason", grabs[0].Message)
	}
	// The release is blocklisted so it can never be grabbed again.
	blocked, err := f.grabs.BlockedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !blocked["guid-spam"] {
		t.Errorf("spam release not blocklisted: %v", blocked)
	}
	// …and a replacement search was kicked off for the book.
	select {
	case id := <-researched:
		if id != f.book.ID {
			t.Errorf("research callback bookID = %d, want %d", id, f.book.ID)
		}
	case <-time.After(2 * time.Second):
		t.Error("replacement search was not triggered")
	}
	// No file was imported.
	if bookFiles, _ := f.store.ListBookFiles(f.book.ID); len(bookFiles) != 0 {
		t.Errorf("spam import produced files: %+v", bookFiles)
	}
	// The junk was removed from the client with its data…
	if len(f.delData) != 1 || f.delData[0] != "nzo_spam" {
		t.Errorf("junk not removed from client with data: delData = %v", f.delData)
	}
	// …and its folder deleted from disk directly (clients that ignore the
	// delete-files flag).
	if _, err := os.Stat(spamDir); !os.IsNotExist(err) {
		t.Errorf("spam folder not deleted from disk: stat err = %v", err)
	}
}

// TestImportAbandonsUnresolvablePath: a completed download whose folder never
// appears at the reported path — while the download area IS reachable — is
// abandoned (failed + blocklisted + re-searched) once past the grace period,
// instead of retrying forever. Covers special-char names that land in
// short-name folders the client reports under a mangled path.
func TestImportAbandonsUnresolvablePath(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	researched := make(chan int64, 1)
	f.svc.OnJunkBlocklist(func(bookID int64, mediaType string) { researched <- bookID })

	// Reported storage points inside a real parent dir, but the folder itself
	// was never created there (it landed under a mangled short name elsewhere).
	parent := t.TempDir()
	missing := filepath.Join(parent, "Mort – Retail (never here)")
	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_stuck", "name": "Terry Pratchett - Mort Retail EPUB",
		"status": "Completed", "storage": missing, "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_stuck",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-stuck",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	// Age the grab past the grace period.
	if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
		"2020-01-01 00:00:00", grabs[0].ID); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	grabs, _ = f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusFailed {
		t.Errorf("grab status = %s, want failed", grabs[0].Status)
	}
	blocked, _ := f.grabs.BlockedKeys()
	if !blocked["guid-stuck"] {
		t.Errorf("unresolvable release not blocklisted: %v", blocked)
	}
	select {
	case id := <-researched:
		if id != f.book.ID {
			t.Errorf("research bookID = %d, want %d", id, f.book.ID)
		}
	case <-time.After(2 * time.Second):
		t.Error("replacement search was not triggered")
	}
}

// TestImportRetriesFreshUnresolvablePath: the same missing-folder situation but
// a *fresh* grab is left pending (retried), not abandoned — a slow share is
// given time to finish syncing.
func TestImportRetriesFreshUnresolvablePath(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	parent := t.TempDir()
	missing := filepath.Join(parent, "Mort – Retail (syncing)")
	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_fresh", "name": "Terry Pratchett - Mort Retail EPUB",
		"status": "Completed", "storage": missing, "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_fresh",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-fresh",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 0 || result.Skipped != 1 {
		t.Fatalf("fresh pending download should be skipped, got %+v", result)
	}
	if grabs, _ := f.grabs.ListGrabs(""); grabs[0].Status != download.GrabStatusGrabbed {
		t.Errorf("fresh grab status = %s, want grabbed (still pending)", grabs[0].Status)
	}
}

// TestImportRetriesEmptyDownloadFolder: a debrid mount can report a torrent
// "seeded" and show its folder before the folder's actual contents finish
// syncing to the share — the download path itself exists and is reachable,
// but nothing is in it yet. A fresh grab must be retried, not abandoned and
// blocklisted, so a good release isn't discarded for a timing accident.
// Reproduces a real bug: a TorBox-backed torrent whose files hadn't synced
// yet got permanently blocklisted seconds after import ran.
func TestImportRetriesEmptyDownloadFolder(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	empty := t.TempDir() // exists, reachable, but has nothing in it yet
	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_empty", "name": "Terry Pratchett - Mort Retail EPUB",
		"status": "Completed", "storage": empty, "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_empty",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-empty",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 0 || result.Skipped != 1 {
		t.Fatalf("empty-but-syncing download should be skipped, not failed: %+v", result)
	}
	if grabs, _ := f.grabs.ListGrabs(""); grabs[0].Status != download.GrabStatusGrabbed {
		t.Errorf("fresh grab status = %s, want grabbed (still pending)", grabs[0].Status)
	}
	if blocked, _ := f.grabs.BlockedKeys(); blocked["guid-empty"] {
		t.Error("a merely-still-syncing download must not be blocklisted")
	}
}

// TestImportGivesUpOnPermanentlyEmptyDownload: the same empty-folder
// situation, but the grab is old enough that it's clearly never going to
// sync — gives up and blocklists rather than retrying forever.
func TestImportGivesUpOnPermanentlyEmptyDownload(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	empty := t.TempDir()
	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_stale_empty", "name": "Terry Pratchett - Mort Retail EPUB",
		"status": "Completed", "storage": empty, "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_stale_empty",
		Title: "Terry Pratchett - Mort Retail EPUB", GUID: "guid-stale-empty",
		Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
		"2020-01-01 00:00:00", grabs[0].ID); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	if grabs, _ = f.grabs.ListGrabs(""); grabs[0].Status != download.GrabStatusFailed {
		t.Errorf("grab status = %s, want failed", grabs[0].Status)
	}
	if blocked, _ := f.grabs.BlockedKeys(); !blocked["guid-stale-empty"] {
		t.Errorf("permanently empty download not blocklisted: %v", blocked)
	}
}

// TestImportDeletesDownloadedFilesWhenEnabled: with DeleteCompletedFiles on, a
// usenet import removes the download from the client WITH its files.
func TestImportDeletesDownloadedFilesWhenEnabled(t *testing.T) {
	f := fixture(t)
	f.deleteFiles = true // implies remove; also delete the source files
	ctx := context.Background()

	dir := f.completedDownload(t, "nzo_del", "Terry Pratchett - Mort Retail EPUB", "Mort.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_del",
		Title: "Terry Pratchett - Mort Retail EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v", result)
	}
	// Client was told to delete the files…
	if len(f.delData) != 1 || f.delData[0] != "nzo_del" {
		t.Errorf("download not deleted with its files: delData = %v", f.delData)
	}
	// …and the source folder is gone from disk (clients that ignore the flag).
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("source download folder not deleted: stat err = %v", err)
	}
}

// TestImportRemovesTorrentFromClientWhenEnabled: with RemoveCompleted on, a
// finished torrent is removed from the client right after import (keeping its
// files), instead of being left to seed.
func TestImportRemovesTorrentFromClientWhenEnabled(t *testing.T) {
	f := fixture(t)
	f.removeCompleted = true // remove from client after import; keep files
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "Mort")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Mort.epub"), []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	var deleted []string
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/login"):
			w.Write([]byte("Ok."))
		case strings.HasSuffix(r.URL.Path, "/torrents/info"):
			// Finished but still seeding (not the paused "seeded" state).
			fmt.Fprintf(w, `[{"hash":"h9","name":"Terry Pratchett - Mort EPUB","state":"uploading","progress":1,"content_path":%q,"category":"librinode"}]`, dir)
		case strings.HasSuffix(r.URL.Path, "/torrents/delete"):
			r.ParseForm()
			deleted = append(deleted, r.FormValue("hashes")+":"+r.FormValue("deleteFiles"))
			w.Write([]byte("Ok."))
		default:
			w.Write([]byte("{}"))
		}
	}))
	t.Cleanup(qbit.Close)
	if err := f.grabs.Add(&download.ClientConfig{
		Name: "qbit", Type: download.TypeQBittorrent, Host: qbit.URL,
		Category: "librinode", Enabled: true, Priority: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 2,
		Title: "Terry Pratchett - Mort EPUB", Protocol: download.ProtocolTorrent,
		MediaType: "ebook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v", result)
	}
	// Removed from the client, but files kept (deleteFiles=false).
	if len(deleted) != 1 || deleted[0] != "h9:false" {
		t.Errorf("torrent removal = %v, want [h9:false]", deleted)
	}
}

func TestImportUntrackedByTitle(t *testing.T) {
	f := fixture(t)

	f.completedDownload(t, "nzo_2", "Mort by Terry Pratchett epub", "mort_v2.epub")

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v (messages: %v)", result, result.Messages)
	}
	book, _ := f.store.GetBook(f.book.ID)
	if !book.HasFile {
		t.Error("book should have gained the untracked download")
	}
}

func TestFailedDownloadResolvesGrab(t *testing.T) {
	f := fixture(t)

	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_bad", "name": "Mort broken", "status": "Failed",
		"fail_message": "crc error", "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_bad",
		Title: "Mort broken", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Imported != 0 {
		t.Fatalf("result = %+v", result)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusFailed {
		t.Errorf("grab = %+v", grabs[0])
	}
	if len(f.removed) != 1 {
		t.Errorf("failed download not removed from client: %v", f.removed)
	}
}

func TestFailedDownloadIsBlocklisted(t *testing.T) {
	f := fixture(t)

	f.history = append(f.history, map[string]any{
		"nzo_id": "nzo_bad2", "name": "Mort broken", "status": "Failed",
		"fail_message": "crc error", "category": "librinode",
	})
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_bad2",
		Title: "Mort broken", GUID: "guid-bad", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := f.grabs.ListBlocklist()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].GUID != "guid-bad" || entries[0].Title != "Mort broken" {
		t.Fatalf("blocklist = %+v", entries)
	}
	// Both keys block, and title matching survives case/spacing changes.
	blocked, _ := f.grabs.BlockedKeys()
	if !download.IsBlocked(blocked, "guid-bad", "") || !download.IsBlocked(blocked, "", "mort  BROKEN") {
		t.Error("blocklist keys don't match by guid/title")
	}
}

func TestImportUpgradeReplacesFile(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	// The book owns a PDF on disk.
	oldPath := filepath.Join(f.rootDir, "Terry Pratchett", "Mort.pdf")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("old-pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook", Path: oldPath, Format: "pdf",
	}); err != nil {
		t.Fatal(err)
	}

	// A tracked grab delivers an EPUB (better per the default profile).
	f.completedDownload(t, "nzo_up", "Terry Pratchett - Mort Retail EPUB", "mort.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_up",
		Title: "Terry Pratchett - Mort Retail EPUB", Protocol: download.ProtocolUsenet,
		MediaType: "ebook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v", result)
	}

	// New epub recorded, old pdf gone from disk and library.
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("upgraded-away pdf still on disk")
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Format != "epub" {
		t.Fatalf("files after upgrade = %+v", files)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusImported ||
		!strings.Contains(grabs[0].Message, "upgraded (pdf → epub)") {
		t.Fatalf("grab = %+v", grabs[0])
	}
}

// TestImportUpgradeMultiFileToSingleFileKeepsNewFile: an owned multi-file
// audiobook (its recorded path is the whole book folder) upgraded by a
// single-file format (m4b) must end up with the new file actually on disk —
// not wiped out by the old-folder cleanup landing on top of it. Regression
// for the bug where RemoveAll(old.Path) deleted the book folder (and the
// just-placed replacement inside it) because the safety check only caught an
// exact path match, not "new file nested inside old directory".
func TestImportUpgradeMultiFileToSingleFileKeepsNewFile(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	// Owned: a multi-file mp3 audiobook, recorded (per the folder-based
	// placement audiobooks use) with the whole book folder as its path.
	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTracks := []string{"Mort - 01.mp3", "Mort - 02.mp3"}
	for _, name := range oldTracks {
		if err := os.WriteFile(filepath.Join(bookDir, name), []byte("old-mp3"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 2, BookID: f.book.ID, MediaType: "audiobook", Path: bookDir, Format: "mp3",
	}); err != nil {
		t.Fatal(err)
	}

	// A tracked grab delivers a single M4B (ranks above mp3 in the default
	// audiobook profile) — a genuine upgrade.
	f.completedDownload(t, "nzo_m4b", "Terry Pratchett - Mort Unabridged", "Mort.m4b")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_m4b",
		Title: "Terry Pratchett - Mort Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v", result)
	}

	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Format != "m4b" {
		t.Fatalf("files after upgrade = %+v", files)
	}

	// The new file must actually exist on disk — this is what the bug broke.
	if _, err := os.Stat(files[0].Path); err != nil {
		t.Fatalf("upgraded m4b missing from disk at %s: %v", files[0].Path, err)
	}
	// The old tracks are gone.
	for _, name := range oldTracks {
		if _, err := os.Stat(filepath.Join(bookDir, name)); !os.IsNotExist(err) {
			t.Errorf("old track %s still on disk", name)
		}
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusImported ||
		!strings.Contains(grabs[0].Message, "upgraded (mp3 → m4b)") {
		t.Fatalf("grab = %+v", grabs[0])
	}
}

func TestImportNotAnUpgradeSkips(t *testing.T) {
	f := fixture(t)

	// The book owns an EPUB; a grabbed PDF must not replace it.
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook",
		Path: filepath.Join(f.rootDir, "m.epub"), Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}
	f.completedDownload(t, "nzo_dn", "Terry Pratchett - Mort PDF", "mort.pdf")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_dn",
		Title: "Terry Pratchett - Mort PDF", Protocol: download.ProtocolUsenet,
		MediaType: "ebook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Skipped == 0 {
		t.Fatalf("result = %+v", result)
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 || files[0].Format != "epub" {
		t.Fatalf("files = %+v", files)
	}
}

func TestImportSkipsWhenNoEbookInDownload(t *testing.T) {
	f := fixture(t)

	f.completedDownload(t, "nzo_3", "Terry Pratchett - Mort Retail EPUB", "readme.txt")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_3",
		Title: "Terry Pratchett - Mort Retail EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 {
		t.Fatalf("result = %+v", result)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusFailed || grabs[0].Message == "" {
		t.Errorf("grab = %+v", grabs[0])
	}
}

func TestAmbiguousUntrackedIsSkipped(t *testing.T) {
	f := fixture(t)

	// Second fileless book that also matches "Mort" releases.
	other := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "2",
		Title: "Mort", Monitored: true}
	// Same title is fine — different foreign id.
	if err := f.store.UpsertBook(other); err != nil {
		t.Fatal(err)
	}
	f.completedDownload(t, "nzo_4", "Mort epub", "mort.epub")

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Skipped == 0 {
		t.Fatalf("ambiguous match should skip: %+v", result)
	}
}

// comicSeries adds a comic series with three volumes (positions 1–3) and a
// comic root folder: vol 1 monitored, vol 2 monitored (the grab target),
// vol 3 unmonitored.
func (f *fx) comicSeries(t *testing.T) (v1, v2, v3 *library.Book) {
	t.Helper()
	series := &library.Series{Source: "hardcover", ForeignID: "7310",
		Title: "Death Note", MediaType: "comic", Monitored: true}
	if err := f.store.UpsertSeries(series); err != nil {
		t.Fatal(err)
	}
	vol := func(fid string, pos float64, monitored bool) *library.Book {
		b := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover",
			ForeignID: fid, MediaType: "comic", Monitored: monitored,
			Title: fmt.Sprintf("Death Note Vol. %.0f", pos)}
		if err := f.store.UpsertBook(b); err != nil {
			t.Fatal(err)
		}
		if err := f.store.LinkBookSeries(b.ID, series.ID, pos); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, err := f.db.Exec(
		`INSERT INTO root_folders (media_type, path) VALUES ('comic', ?)`,
		t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return vol("dn1", 1, true), vol("dn2", 2, true), vol("dn3", 3, false)
}

// TestPackImportsMonitoredVolumesOnly: grabbing one volume from a
// complete-series bundle imports the grabbed volume (matched by number, not
// size) plus any other *monitored* volumes — never the unmonitored ones.
func TestPackImportsMonitoredVolumesOnly(t *testing.T) {
	f := fixture(t)
	v1, v2, v3 := f.comicSeries(t)

	// The unmonitored volume's file is the largest in the bundle — size must
	// not decide which file the grabbed volume gets.
	f.completedDownload(t, "nzo_pack", "Death Note v01-v03 Complete Digital",
		"Death Note v01.cbz",
		"Death Note v02.cbz",
		"Death Note v03 Extended Collectors Special Edition.cbz")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: v2.ID, MediaType: "comic", ClientConfigID: 1, ClientItemID: "nzo_pack",
		Title: "Death Note v01-v03 Complete Digital", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (grabbed v2 + monitored v1): %+v", result.Imported, result)
	}

	// The grabbed volume got ITS file (v02), not the bundle's largest (v03).
	files, _ := f.store.ListBookFiles(v2.ID)
	if len(files) != 1 {
		t.Fatalf("v2 files = %+v", files)
	}
	if got, _ := os.ReadFile(files[0].Path); string(got) != "book-bytes-Death Note v02.cbz" {
		t.Fatalf("v2 imported the wrong source file: %q", got)
	}

	// Monitored v1 came along; unmonitored v3 did not.
	if files, _ := f.store.ListBookFiles(v1.ID); len(files) != 1 {
		t.Fatalf("v1 files = %+v, want the pack extra", files)
	}
	if files, _ := f.store.ListBookFiles(v3.ID); len(files) != 0 {
		t.Fatalf("v3 files = %+v, want none (unmonitored)", files)
	}

	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusImported {
		t.Errorf("grab = %+v", grabs[0])
	}
}

// TestPackEbookImportsMonitoredByTitle: an ebook bundle fills the author's
// monitored books by title match; unmonitored books are left alone.
func TestPackEbookImportsMonitoredByTitle(t *testing.T) {
	f := fixture(t)

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}
	sourcery := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "11",
		Title: "Sourcery"} // enrolled nowhere, monitored nowhere
	if err := f.store.UpsertBook(sourcery); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_epack", "Terry Pratchett - Discworld Collection EPUB",
		"Mort.epub", "Guards! Guards! (1989).epub", "Sourcery.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_epack",
		Title: "Terry Pratchett - Discworld Collection EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (grabbed Mort + monitored Guards): %+v", result.Imported, result)
	}

	// The grabbed book got its own file, not another book's.
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 {
		t.Fatalf("Mort files = %+v", files)
	}
	if got, _ := os.ReadFile(files[0].Path); string(got) != "book-bytes-Mort.epub" {
		t.Fatalf("Mort imported the wrong source file: %q", got)
	}
	if files, _ := f.store.ListBookFiles(guards.ID); len(files) != 1 {
		t.Fatalf("Guards files = %+v, want the pack extra", files)
	}
	if files, _ := f.store.ListBookFiles(sourcery.ID); len(files) != 0 {
		t.Fatalf("Sourcery files = %+v, want none (unmonitored)", files)
	}
}

// TestPackImportAllOptIn: with the pack-import-all setting on, the pack fills
// unmonitored books too — and enrolled ebooks join their format library.
func TestPackImportAllOptIn(t *testing.T) {
	f := fixture(t)
	f.packAll = true
	v1, v2, v3 := f.comicSeries(t)

	f.completedDownload(t, "nzo_all", "Death Note v01-v03 Complete Digital",
		"Death Note v01.cbz", "Death Note v02.cbz", "Death Note v03.cbz")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: v2.ID, MediaType: "comic", ClientConfigID: 1, ClientItemID: "nzo_all",
		Title: "Death Note v01-v03 Complete Digital", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 3 {
		t.Fatalf("imported = %d, want 3 (import-all fills the unmonitored volume too): %+v",
			result.Imported, result)
	}
	for _, v := range []*library.Book{v1, v2, v3} {
		if files, _ := f.store.ListBookFiles(v.ID); len(files) != 1 {
			t.Fatalf("%s files = %+v, want 1", v.Title, files)
		}
	}
	// The unmonitored volume stays unmonitored — owning it never re-monitors.
	got, _ := f.store.GetBook(v3.ID)
	if got.Monitored {
		t.Error("import-all must not monitor the unmonitored volume")
	}
}

// TestPackImportAllEnrollsEbook: import-all puts an unenrolled prose book's
// file in place AND makes the book an ebook-library member (like scan does).
func TestPackImportAllEnrollsEbook(t *testing.T) {
	f := fixture(t)
	f.packAll = true

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!"} // not enrolled, not monitored
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_eall", "Terry Pratchett - Two Book Bundle EPUB",
		"Mort.epub", "Guards! Guards!.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_eall",
		Title: "Terry Pratchett - Two Book Bundle EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2: %+v", result.Imported, result)
	}
	got, _ := f.store.GetBook(guards.ID)
	if !got.InEbookLibrary {
		t.Error("import-all should enroll the imported ebook in the library")
	}
	if got.EbookMonitored {
		t.Error("import-all must not monitor the book")
	}
}

// TestPackSkipsOwnedBookUnlessUpgrade: a monitored book that already owns the
// format is not re-imported from a pack (same format is not an upgrade).
func TestPackSkipsOwnedBookUnlessUpgrade(t *testing.T) {
	f := fixture(t)

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(t.TempDir(), "guards.epub")
	if err := os.WriteFile(owned, []byte("already-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: guards.ID, MediaType: "ebook",
		Path: owned, Size: 13, Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_epack2", "Terry Pratchett - Two Book Bundle EPUB",
		"Mort.epub", "Guards! Guards!.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_epack2",
		Title: "Terry Pratchett - Two Book Bundle EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("imported = %d, want 1 (Guards owns an equal-quality epub): %+v", result.Imported, result)
	}
	files, _ := f.store.ListBookFiles(guards.ID)
	if len(files) != 1 || files[0].Path != owned {
		t.Fatalf("Guards files = %+v, want only the pre-owned epub", files)
	}
}

// TestPackFillsOtherBooksWhenGrabbedBookAlreadyOwned: the grabbed book
// already owning a non-upgradeable file must not stop the pack's OTHER
// monitored books from importing — skipping the primary book is not the
// same as skipping the whole pack.
func TestPackFillsOtherBooksWhenGrabbedBookAlreadyOwned(t *testing.T) {
	f := fixture(t)

	owned := filepath.Join(t.TempDir(), "mort.epub")
	if err := os.WriteFile(owned, []byte("already-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertBookFile(&library.BookFile{
		RootFolderID: 1, BookID: f.book.ID, MediaType: "ebook",
		Path: owned, Size: 13, Format: "epub",
	}); err != nil {
		t.Fatal(err)
	}

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_epack3", "Terry Pratchett - Two Book Bundle EPUB",
		"Mort.epub", "Guards! Guards!.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_epack3",
		Title: "Terry Pratchett - Two Book Bundle EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("imported = %d, want 1 (Guards imported; Mort skipped, not an upgrade): %+v", result.Imported, result)
	}
	// Mort keeps its pre-owned file untouched.
	mortFiles, _ := f.store.ListBookFiles(f.book.ID)
	if len(mortFiles) != 1 || mortFiles[0].Path != owned {
		t.Fatalf("Mort files = %+v, want only the pre-owned epub", mortFiles)
	}
	// Guards still got filled in from the same pack.
	if files, _ := f.store.ListBookFiles(guards.ID); len(files) != 1 {
		t.Fatalf("Guards files = %+v, want the pack extra", files)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if grabs[0].Status != download.GrabStatusImported || !strings.Contains(grabs[0].Message, "not an upgrade") {
		t.Errorf("grab = %+v", grabs[0])
	}
}

// TestPackMatchNotAmbiguousWhenOtherBookTitleIsSubstring: a short book title
// that's a literal substring/prefix of another of the author's book titles
// ("Dune" inside "Dune Messiah" — the real case this reproduces) must not
// make a release named after the longer title alone look ambiguous, or look
// like it's still waiting on a "Dune"-sized sibling that was never actually
// part of this download. Before bestBookMatches existed, match() saw "Storm"
// as matching first, then found "Storm Warning" also matched under a
// different book ID and bailed out as ambiguous — so the grabbed book's own
// file was never recognized, and the pack held it back forever waiting for
// a sibling that could never arrive.
func TestPackMatchNotAmbiguousWhenOtherBookTitleIsSubstring(t *testing.T) {
	f := fixture(t)

	short := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "20",
		Title: "Storm", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(short); err != nil {
		t.Fatal(err)
	}
	long := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "21",
		Title: "Storm Warning", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(long); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_storm", "Terry Pratchett - Storm Warning (2010) epub",
		"Storm Warning.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: long.ID, ClientConfigID: 1, ClientItemID: "nzo_storm",
		Title: "Terry Pratchett - Storm Warning (2010) epub", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v, want Storm Warning imported (not mistaken for an ambiguous/incomplete pack)", result)
	}
	if files, _ := f.store.ListBookFiles(long.ID); len(files) != 1 {
		t.Errorf("Storm Warning files = %+v, want 1", files)
	}
	if files, _ := f.store.ListBookFiles(short.ID); len(files) != 0 {
		t.Errorf("Storm files = %+v, want none (it was never in this download)", files)
	}
}

// TestPackMatchNotAmbiguousForDuplicateSplitEditionEntry: a messy
// bibliography can carry duplicate/split-edition rows for the same book
// ("Dune Messiah (1 of 2)", "Dune Messiah (2 of 2)") whose title, once
// TitleKeys strips the trailing parenthetical, is identical to the real
// book's own title. Before bestBookMatches preferred a primary-title match
// over a same-text fallback-key match, these duplicates tied with the real
// book on every one of its own releases and match() bailed out as
// ambiguous — so the file was never recognized as belonging to the book at
// all, and a plain single-book download was held back forever waiting for
// siblings that were never real.
func TestPackMatchNotAmbiguousForDuplicateSplitEditionEntry(t *testing.T) {
	f := fixture(t)

	real := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "30",
		Title: "Dune Messiah", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(real); err != nil {
		t.Fatal(err)
	}
	part1 := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "31",
		Title: "Dune Messiah (1 of 2)"}
	if err := f.store.UpsertBook(part1); err != nil {
		t.Fatal(err)
	}
	part2 := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "32",
		Title: "Dune Messiah (2 of 2)"}
	if err := f.store.UpsertBook(part2); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_dune", "Herbert, Frank - Dune Messiah (1965) english epub",
		"Dune Messiah.epub")
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: real.ID, ClientConfigID: 1, ClientItemID: "nzo_dune",
		Title: "Herbert, Frank - Dune Messiah (1965) english epub", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v, want Dune Messiah imported (not stuck behind a false duplicate-entry ambiguity)", result)
	}
	if files, _ := f.store.ListBookFiles(real.ID); len(files) != 1 {
		t.Errorf("Dune Messiah files = %+v, want 1", files)
	}
}

// TestPackEbookWaitsForTitlePromisedSiblingFile: the same debrid sync-delay
// problem audiobook packs guard against, but for ebooks: a multi-file
// download can populate one book's file before the other's has arrived, and
// nothing about the single file present says whether this is a genuine
// single-book release or a pack still filling in. The release's own title
// naming a second one of this author's books is the only independent signal,
// and a fresh grab must wait for it rather than importing early.
func TestPackEbookWaitsForTitlePromisedSiblingFile(t *testing.T) {
	f := fixture(t)

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	// Only Mort's file has synced so far; Guards! Guards! hasn't appeared on
	// disk at all yet — but the release title names both.
	f.completedDownload(t, "nzo_ewaiting", "Terry Pratchett - Mort & Guards! Guards! EPUB",
		"Mort.epub",
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_ewaiting",
		Title: "Terry Pratchett - Mort & Guards! Guards! EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Failed != 0 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want skipped (waiting for Guards! Guards! to appear)", result)
	}
	if grabs, _ := f.grabs.ListGrabs(""); grabs[0].Status != download.GrabStatusGrabbed {
		t.Errorf("grab status = %s, want grabbed (still pending)", grabs[0].Status)
	}
	if files, _ := f.store.ListBookFiles(f.book.ID); len(files) != 0 {
		t.Error("Mort must not import early — it's part of a pack still waiting on its sibling")
	}
}

// TestPackEbookGivesUpAfterGraceAndImportsWhatSynced: same setup, but the
// grab is old enough that waiting stops being reasonable — imports the one
// book that did sync rather than holding a good release hostage forever
// because its sibling never showed up.
func TestPackEbookGivesUpAfterGraceAndImportsWhatSynced(t *testing.T) {
	f := fixture(t)

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InEbookLibrary: true, EbookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_egaveup", "Terry Pratchett - Mort & Guards! Guards! EPUB",
		"Mort.epub",
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_egaveup",
		Title: "Terry Pratchett - Mort & Guards! Guards! EPUB", Protocol: download.ProtocolUsenet,
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
		"2020-01-01 00:00:00", grabs[0].ID); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v, want Mort imported (Guards! Guards! never showed up)", result)
	}
	files, _ := f.store.ListBookFiles(f.book.ID)
	if len(files) != 1 {
		t.Error("Mort should have imported after giving up on its never-appearing sibling")
	}
	if files, _ := f.store.ListBookFiles(guards.ID); len(files) != 0 {
		t.Errorf("Guards! Guards! files = %+v, want none (it never synced)", files)
	}
}

// TestImportAudiobookPackWaitsForTitlePromisedSiblingFolder: a debrid mount
// can populate a multi-book download's folders one at a time — nothing on
// disk tells "this genuinely is a single-book release" apart from "the
// pack's other book folder just hasn't appeared yet", since both look
// identical (one folder, fully synced) at that instant. The release's own
// title — naming both books, joined by "&", the same shape as a real
// AudioBookBay bundle — is the only independent signal there should be a
// second folder at all, and a fresh grab must wait for it rather than
// importing the one visible folder as an ordinary single book.
func TestImportAudiobookPackWaitsForTitlePromisedSiblingFolder(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}
	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InAudiobookLibrary: true, AudiobookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	// Only Mort's folder has synced so far; Guards! Guards! hasn't appeared
	// on disk at all yet — but the release title names both.
	f.completedDownload(t, "nzo_waiting", "Terry Pratchett - Mort & Guards! Guards! Unabridged",
		filepath.Join("Mort", "01 - Opening.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_waiting",
		Title: "Terry Pratchett - Mort & Guards! Guards! Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Failed != 0 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want skipped (waiting for Guards! Guards! to appear)", result)
	}
	if grabs, _ := f.grabs.ListGrabs(""); grabs[0].Status != download.GrabStatusGrabbed {
		t.Errorf("grab status = %s, want grabbed (still pending)", grabs[0].Status)
	}
	if f.book.ID != 0 {
		if got, _ := f.store.GetBook(f.book.ID); got.HasAudiobookFile {
			t.Error("Mort must not import early — it's part of a pack still waiting on its sibling")
		}
	}
}

// TestImportAudiobookPackGivesUpAfterGraceAndImportsWhatSynced: the same
// situation, but the grab is old enough that waiting stops being reasonable
// — imports the one book that did sync rather than holding a good release
// hostage forever because its sibling never showed up.
func TestImportAudiobookPackGivesUpAfterGraceAndImportsWhatSynced(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}
	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", InAudiobookLibrary: true, AudiobookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_gaveup", "Terry Pratchett - Mort & Guards! Guards! Unabridged",
		filepath.Join("Mort", "01 - Opening.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_gaveup",
		Title: "Terry Pratchett - Mort & Guards! Guards! Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}
	grabs, _ := f.grabs.ListGrabs("")
	if _, err := f.db.Exec("UPDATE grabs SET grabbed_at = ? WHERE id = ?",
		"2020-01-01 00:00:00", grabs[0].ID); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("result = %+v, want Mort imported (Guards! Guards! never showed up)", result)
	}
	got, _ := f.store.GetBook(f.book.ID)
	if !got.HasAudiobookFile {
		t.Error("Mort should have imported after giving up on its never-appearing sibling")
	}
	if files, _ := f.store.ListBookFiles(guards.ID); len(files) != 0 {
		t.Errorf("Guards! Guards! files = %+v, want none (it never synced)", files)
	}
}

// TestImportAudiobookPackFillsMonitoredBooksByFolderName: a bundle organizing
// each book into its own top-level subfolder imports the grabbed book
// (matched by folder name, not size or any individual track's filename) plus
// any other *monitored* audiobook — never an unmonitored one.
func TestImportAudiobookPackFillsMonitoredBooksByFolderName(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	guards := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "10",
		Title: "Guards! Guards!", ReleaseDate: "1989-01-01", InAudiobookLibrary: true, AudiobookMonitored: true}
	if err := f.store.UpsertBook(guards); err != nil {
		t.Fatal(err)
	}
	sourcery := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "11",
		Title: "Sourcery"} // enrolled nowhere, monitored nowhere
	if err := f.store.UpsertBook(sourcery); err != nil {
		t.Fatal(err)
	}

	// The largest, most generically-named tracks belong to the book that must
	// NOT be picked for the grab — proves matching goes by folder name, not size.
	f.completedDownload(t, "nzo_abpack", "Terry Pratchett - Discworld Collection Unabridged",
		filepath.Join("Mort", "01 - Opening.mp3"),
		filepath.Join("Mort", "02 - Death.mp3"),
		filepath.Join("Guards! Guards!", "01 - Theft.mp3"),
		filepath.Join("Sourcery", "01 - Rincewind.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_abpack",
		Title: "Terry Pratchett - Discworld Collection Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (grabbed Mort + monitored Guards): %+v", result.Imported, result)
	}

	mortDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, name := range []string{"01 - Opening.mp3", "02 - Death.mp3"} {
		if _, err := os.Stat(filepath.Join(mortDir, name)); err != nil {
			t.Errorf("Mort track missing: %v", err)
		}
	}
	if files, _ := f.store.ListBookFiles(guards.ID); len(files) != 1 {
		t.Fatalf("Guards files = %+v, want the pack extra", files)
	}
	if files, _ := f.store.ListBookFiles(sourcery.ID); len(files) != 0 {
		t.Fatalf("Sourcery files = %+v, want none (unmonitored)", files)
	}
}

// TestImportAudiobookPackImportAllOptIn: with the pack-import-all setting on,
// an audiobook pack fills unmonitored books too, without monitoring them.
func TestImportAudiobookPackImportAllOptIn(t *testing.T) {
	f := fixture(t)
	f.packAll = true
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	sourcery := &library.Book{AuthorID: f.book.AuthorID, Source: "hardcover", ForeignID: "11",
		Title: "Sourcery"} // enrolled nowhere, monitored nowhere
	if err := f.store.UpsertBook(sourcery); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_aball", "Terry Pratchett - Discworld Collection Unabridged",
		filepath.Join("Mort", "01 - Opening.mp3"),
		filepath.Join("Sourcery", "01 - Rincewind.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_aball",
		Title: "Terry Pratchett - Discworld Collection Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 {
		t.Fatalf("imported = %d, want 2 (import-all fills the unmonitored book too): %+v", result.Imported, result)
	}
	if files, _ := f.store.ListBookFiles(sourcery.ID); len(files) != 1 {
		t.Fatalf("Sourcery files = %+v, want the pack extra", files)
	}
	got, _ := f.store.GetBook(sourcery.ID)
	if got.AudiobookMonitored {
		t.Error("import-all must not monitor the unmonitored book")
	}
}

// TestImportAudiobookSingleSubfolderIsNotAPack: exactly one top-level
// subfolder isn't a detectable pack — indistinguishable from an ordinary
// single-book release organized in its own folder — so it imports as one
// book, all its files together, same as before pack support existed.
func TestImportAudiobookSingleSubfolderIsNotAPack(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_single", "Terry Pratchett - Mort Unabridged",
		filepath.Join("Mort", "01 - Opening.mp3"),
		filepath.Join("Mort", "02 - Death.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_single",
		Title: "Terry Pratchett - Mort Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("imported = %d, want 1: %+v", result.Imported, result)
	}
	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, name := range []string{"01 - Opening.mp3", "02 - Death.mp3"} {
		if _, err := os.Stat(filepath.Join(bookDir, name)); err != nil {
			t.Errorf("track missing: %v", err)
		}
	}
}

// TestImportAudiobookPackFallsBackWhenGrabbedFolderUnmatched: if none of the
// pack's folder names match the grabbed book by title, the whole download
// falls back to being treated as the one book rather than failing outright
// or guessing wrong.
func TestImportAudiobookPackFallsBackWhenGrabbedFolderUnmatched(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()

	abRoot := t.TempDir()
	if _, err := f.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('audiobook', ?)`, abRoot); err != nil {
		t.Fatal(err)
	}

	f.completedDownload(t, "nzo_nomatch", "Terry Pratchett - Mort Unabridged",
		filepath.Join("Unknown Group A", "01 - Opening.mp3"),
		filepath.Join("Unknown Group B", "01 - Continued.mp3"),
	)
	if err := f.grabs.AddGrab(&download.GrabRecord{
		BookID: f.book.ID, ClientConfigID: 1, ClientItemID: "nzo_nomatch",
		Title: "Terry Pratchett - Mort Unabridged", Protocol: download.ProtocolUsenet,
		MediaType: "audiobook",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := f.svc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("imported = %d, want 1 (fallback to single book): %+v", result.Imported, result)
	}
	// Non-disc subfolders flatten (same as an ordinary single-book download —
	// see TestImportAudiobookFlattensNonDiscNesting); the two names don't
	// collide, so neither gets qualified with its folder.
	bookDir := filepath.Join(abRoot, "Terry Pratchett", "Mort (1987)")
	for _, name := range []string{"01 - Opening.mp3", "01 - Continued.mp3"} {
		if _, err := os.Stat(filepath.Join(bookDir, name)); err != nil {
			t.Errorf("track missing: %v", err)
		}
	}
}

// TestRunSerializesConcurrentPasses: the periodic sweep and a manual
// "Import now" click both call Run, and nothing previously stopped them
// overlapping. A pack import's cleanup step (RemoveAll-ing the whole
// download folder once its books are copied out) racing against a second,
// still-in-progress pass that's mid-way through copying that same
// download's OTHER book would corrupt or silently drop that book's file —
// exactly the "only one of the two books ever imports, inconsistently
// which one" bug this guards against. Run must serialize against any
// already-in-progress pass rather than let two run concurrently.
func TestRunSerializesConcurrentPasses(t *testing.T) {
	f := fixture(t)

	f.svc.runMu.Lock() // simulate a pass already in progress
	done := make(chan struct{})
	go func() {
		f.svc.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Run returned while another pass held the lock — passes are not serialized")
	case <-time.After(100 * time.Millisecond):
		// still blocked, as expected
	}

	f.svc.runMu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run never completed after the lock was released")
	}
}
