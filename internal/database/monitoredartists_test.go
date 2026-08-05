package database

import (
	"testing"
	"time"
)

func TestCreateAndGetMonitoredArtist(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("CreateMonitoredArtist: %v", err)
	}
	if m.ID == 0 {
		t.Error("expected nonzero ID")
	}
	if m.LastSyncedAt != nil {
		t.Error("LastSyncedAt should be nil until a sync happens")
	}

	got, err := db.GetMonitoredArtist(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMonitoredArtist: %v", err)
	}
	if got.Name != "Boards of Canada" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestCreateMonitoredArtistDuplicateMBIDFails(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	if _, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist"); err == nil {
		t.Error("expected an error monitoring the same artist twice")
	}
}

func TestListMonitoredArtistsEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	list, err := db.ListMonitoredArtists(t.Context())
	if err != nil {
		t.Fatalf("ListMonitoredArtists: %v", err)
	}
	if list == nil {
		t.Error("ListMonitoredArtists returned nil for an empty result, want a non-nil empty slice")
	}
}

func TestSetMonitoredArtistSynced(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetMonitoredArtistSynced(ctx, m.ID, now); err != nil {
		t.Fatalf("SetMonitoredArtistSynced: %v", err)
	}

	got, err := db.GetMonitoredArtist(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncedAt == nil {
		t.Fatal("LastSyncedAt should be set")
	}
}

func TestDeleteMonitoredArtistCascadesWantedAlbums(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	m, err := db.CreateMonitoredArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, m.ID, "rg-mbid", "Album", "Album", "2020")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteMonitoredArtist(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMonitoredArtist: %v", err)
	}
	if _, err := db.GetWantedAlbum(ctx, w.ID); err != ErrNotFound {
		t.Errorf("wanted album should cascade-delete, GetWantedAlbum err = %v, want ErrNotFound", err)
	}
}
