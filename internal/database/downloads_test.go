package database

import (
	"testing"
	"time"
)

func setupWantedAlbum(t *testing.T, db *DB) (*WantedAlbum, *RootFolder) {
	t.Helper()
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, a.ID, "rg-mbid", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}
	rf, err := db.CreateRootFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return w, rf
}

func TestCreateAndGetDownload(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	w, rf := setupWantedAlbum(t, db)

	d, err := db.CreateDownload(ctx, w.ID, rf.ID, ProtocolTorrent, "abc123hash", "Artist - Album [FLAC]", "SomeIndexer")
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if d.Status != DownloadStatusDownloading {
		t.Errorf("Status = %q, want downloading", d.Status)
	}

	got, err := db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.ClientID != "abc123hash" || got.Protocol != ProtocolTorrent {
		t.Errorf("got ClientID=%q Protocol=%q", got.ClientID, got.Protocol)
	}
	if got.CompletedAt != nil || got.ImportedAt != nil {
		t.Error("CompletedAt/ImportedAt should be nil for a fresh download")
	}
}

func TestDownloadLifecycleTransitions(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	w, rf := setupWantedAlbum(t, db)
	d, err := db.CreateDownload(ctx, w.ID, rf.ID, ProtocolUsenet, "nzo123", "Title", "Indexer")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetDownloadCompleted(ctx, d.ID, now); err != nil {
		t.Fatalf("SetDownloadCompleted: %v", err)
	}
	got, err := db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DownloadStatusCompleted || got.CompletedAt == nil {
		t.Errorf("after SetDownloadCompleted: status=%q completedAt=%v", got.Status, got.CompletedAt)
	}

	if err := db.SetDownloadImported(ctx, d.ID, now); err != nil {
		t.Fatalf("SetDownloadImported: %v", err)
	}
	got, err = db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DownloadStatusImported || got.ImportedAt == nil {
		t.Errorf("after SetDownloadImported: status=%q importedAt=%v", got.Status, got.ImportedAt)
	}
}

func TestSetDownloadError(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	w, rf := setupWantedAlbum(t, db)
	d, err := db.CreateDownload(ctx, w.ID, rf.ID, ProtocolTorrent, "hash", "Title", "Indexer")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.SetDownloadError(ctx, d.ID, "tracker unreachable"); err != nil {
		t.Fatalf("SetDownloadError: %v", err)
	}
	got, err := db.GetDownload(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DownloadStatusError || got.ErrorMessage != "tracker unreachable" {
		t.Errorf("got Status=%q ErrorMessage=%q", got.Status, got.ErrorMessage)
	}
}

func TestListDownloadsByStatusAndAll(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	w, rf := setupWantedAlbum(t, db)
	d1, err := db.CreateDownload(ctx, w.ID, rf.ID, ProtocolTorrent, "hash1", "One", "Indexer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateDownload(ctx, w.ID, rf.ID, ProtocolUsenet, "nzo2", "Two", "Indexer"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDownloadCompleted(ctx, d1.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	downloading, err := db.ListDownloadsByStatus(ctx, DownloadStatusDownloading)
	if err != nil {
		t.Fatalf("ListDownloadsByStatus: %v", err)
	}
	if len(downloading) != 1 {
		t.Errorf("downloading = %+v, want 1", downloading)
	}

	all, err := db.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all = %+v, want 2", all)
	}
}

func TestGetDownloadNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetDownload(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
