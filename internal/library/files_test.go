package library

import "testing"

// TestBookFileBatchCommitsAndRollsBack: BookFileBatch must behave exactly
// like the equivalent Store calls when committed, and leave no trace at all
// when rolled back — the whole point of batching a scan's writes into one
// transaction is that it's invisible to correctness, only to how many WAL
// commits it costs.
func TestBookFileBatchCommitsAndRollsBack(t *testing.T) {
	s := newTestStore(t)

	author := testAuthor()
	if err := s.UpsertAuthor(author); err != nil {
		t.Fatal(err)
	}
	book := &Book{AuthorID: author.ID, Source: "hardcover", ForeignID: "b1", Title: "Mort", Monitored: true}
	if err := s.UpsertBook(book); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('ebook', '/roots/ebook')`); err != nil {
		t.Fatal(err)
	}

	// A rolled-back batch leaves no book_file row and no library membership.
	batch, err := s.BeginBookFileBatch()
	if err != nil {
		t.Fatal(err)
	}
	f := &BookFile{RootFolderID: 1, BookID: book.ID, MediaType: "ebook", Path: "/roots/ebook/Mort.epub", Format: "epub"}
	if err := batch.UpsertBookFile(f); err != nil {
		t.Fatal(err)
	}
	batch.Rollback()

	if files, err := s.ListBookFiles(book.ID); err != nil || len(files) != 0 {
		t.Fatalf("after rollback: files = %+v (err %v), want none", files, err)
	}
	if b, err := s.GetBook(book.ID); err != nil || b.InEbookLibrary {
		t.Fatalf("after rollback: InEbookLibrary = %v, want false (nothing should have committed)", b.InEbookLibrary)
	}

	// A committed batch persists the file AND its side effect (library
	// membership via EnsureBookLibrary) — same as the unbatched call.
	batch2, err := s.BeginBookFileBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := batch2.UpsertBookFile(f); err != nil {
		t.Fatal(err)
	}
	if err := batch2.Commit(); err != nil {
		t.Fatal(err)
	}
	// Rollback after a successful Commit must be a harmless no-op (the
	// defer batch.Rollback() pattern every scan*Root function uses).
	batch2.Rollback()

	files, err := s.ListBookFiles(book.ID)
	if err != nil || len(files) != 1 || files[0].Path != f.Path {
		t.Fatalf("after commit: files = %+v (err %v), want one matching Mort.epub", files, err)
	}
	b, err := s.GetBook(book.ID)
	if err != nil || !b.InEbookLibrary {
		t.Fatalf("after commit: InEbookLibrary = %v, want true", b.InEbookLibrary)
	}

	// DeleteBookFile inside a batch works the same way.
	batch3, err := s.BeginBookFileBatch()
	if err != nil {
		t.Fatal(err)
	}
	if err := batch3.DeleteBookFile(files[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := batch3.Commit(); err != nil {
		t.Fatal(err)
	}
	if files, err := s.ListBookFiles(book.ID); err != nil || len(files) != 0 {
		t.Fatalf("after batch delete: files = %+v (err %v), want none", files, err)
	}
}
