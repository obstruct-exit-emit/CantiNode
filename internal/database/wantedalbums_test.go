package database

import "testing"

func TestGetOrCreateWantedAlbumCreatesThenReuses(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	w1, err := db.GetOrCreateWantedAlbum(ctx, a.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum: %v", err)
	}
	if w1.Status != WantedStatusWanted {
		t.Errorf("Status = %q, want wanted", w1.Status)
	}

	w2, err := db.GetOrCreateWantedAlbum(ctx, a.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum (second call): %v", err)
	}
	if w2.ID != w1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", w2.ID, w1.ID)
	}
}

func TestListWantedAlbumsByArtistAndStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(ctx, a.ID, "rg-mbid", "Geogaddi", "Album", "2002")
	if err != nil {
		t.Fatal(err)
	}

	byArtist, err := db.ListWantedAlbumsByArtist(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListWantedAlbumsByArtist: %v", err)
	}
	if len(byArtist) != 1 || byArtist[0].ID != w.ID {
		t.Errorf("byArtist = %+v", byArtist)
	}

	byStatus, err := db.ListWantedAlbumsByStatus(ctx, WantedStatusWanted)
	if err != nil {
		t.Fatalf("ListWantedAlbumsByStatus: %v", err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != w.ID {
		t.Errorf("byStatus = %+v", byStatus)
	}

	if err := db.SetWantedAlbumStatus(ctx, w.ID, WantedStatusDownloading); err != nil {
		t.Fatalf("SetWantedAlbumStatus: %v", err)
	}
	stillWanted, err := db.ListWantedAlbumsByStatus(ctx, WantedStatusWanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillWanted) != 0 {
		t.Errorf("stillWanted = %+v, want empty after status change", stillWanted)
	}
}

func TestGetWantedAlbumNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetWantedAlbum(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
