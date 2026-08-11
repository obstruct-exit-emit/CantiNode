package musiclibrary

import "testing"

func TestGetOrCreateWantedAlbumCreatesThenReuses(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	w1, err := db.GetOrCreateWantedAlbum(a.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum: %v", err)
	}
	if w1.Status != WantedStatusWanted {
		t.Errorf("Status = %q, want wanted", w1.Status)
	}

	w2, err := db.GetOrCreateWantedAlbum(a.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum (second call): %v", err)
	}
	if w2.ID != w1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", w2.ID, w1.ID)
	}
}

func TestListWantedAlbumsByArtistAndStatus(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(a.ID, "rg-mbid", "Geogaddi", "Album", "2002")
	if err != nil {
		t.Fatal(err)
	}

	byArtist, err := db.ListWantedAlbumsByArtist(a.ID)
	if err != nil {
		t.Fatalf("ListWantedAlbumsByArtist: %v", err)
	}
	if len(byArtist) != 1 || byArtist[0].ID != w.ID {
		t.Errorf("byArtist = %+v", byArtist)
	}

	byStatus, err := db.ListWantedAlbumsByStatus(WantedStatusWanted)
	if err != nil {
		t.Fatalf("ListWantedAlbumsByStatus: %v", err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != w.ID {
		t.Errorf("byStatus = %+v", byStatus)
	}

	if err := db.SetWantedAlbumStatus(w.ID, WantedStatusDownloading); err != nil {
		t.Fatalf("SetWantedAlbumStatus: %v", err)
	}
	stillWanted, err := db.ListWantedAlbumsByStatus(WantedStatusWanted)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillWanted) != 0 {
		t.Errorf("stillWanted = %+v, want empty after status change", stillWanted)
	}
}

func TestGetWantedAlbumNotFound(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.GetWantedAlbum(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestDeleteWantedAlbumReturnsItToMissing is the regression case for a real
// bug: wanting an album, then removing it, must free the release group back
// up for Missing — leaving a lingering row behind (the old "ignored" status
// did exactly this) permanently strands it in neither list.
func TestDeleteWantedAlbumReturnsItToMissing(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceArtistReleaseGroups(a.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-mbid", Title: "Geogaddi", PrimaryType: "Album", FirstReleaseDate: "2002-02-04"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}

	missing, err := db.ListMissingArtistReleaseGroups(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("before wanting: missing = %+v, want 1 entry", missing)
	}

	w, err := db.GetOrCreateWantedAlbum(a.ID, "rg-mbid", "Geogaddi", "Album", "2002-02-04")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum: %v", err)
	}
	missing, err = db.ListMissingArtistReleaseGroups(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("after wanting: missing = %+v, want empty (it's wanted now)", missing)
	}

	if err := db.DeleteWantedAlbum(w.ID); err != nil {
		t.Fatalf("DeleteWantedAlbum: %v", err)
	}
	missing, err = db.ListMissingArtistReleaseGroups(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].ReleaseGroupMBID != "rg-mbid" {
		t.Errorf("after removing the wanted album: missing = %+v, want the release group back", missing)
	}

	if _, err := db.GetWantedAlbum(w.ID); err != ErrNotFound {
		t.Errorf("GetWantedAlbum after delete: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteWantedAlbumNotFound(t *testing.T) {
	db := newTestStore(t)
	if err := db.DeleteWantedAlbum(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestClaimWantedAlbumForDownloadIsCompareAndSwap is the regression test
// for the race ClaimWantedAlbumForDownload exists to close: two callers
// (a manual grab and the automatic wanted-list sweep, most realistically)
// both reading "still wanted" and both proceeding to grab. Only the first
// claim on a given row may succeed; every claim after that — regardless
// of how many — must see claimed=false until the status is reset back to
// wanted.
func TestClaimWantedAlbumForDownloadIsCompareAndSwap(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.GetOrCreateWantedAlbum(a.ID, "rg-mbid", "Geogaddi", "Album", "2002")
	if err != nil {
		t.Fatal(err)
	}

	first, err := db.ClaimWantedAlbumForDownload(w.ID)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !first {
		t.Fatal("first claim should succeed on a still-wanted album")
	}

	got, err := db.GetWantedAlbum(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WantedStatusDownloading {
		t.Errorf("status after claim = %q, want downloading", got.Status)
	}

	second, err := db.ClaimWantedAlbumForDownload(w.ID)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second {
		t.Error("second claim on an already-downloading album should fail")
	}

	// Reverting to wanted (e.g. after a failed grab) re-opens the claim.
	if err := db.SetWantedAlbumStatus(w.ID, WantedStatusWanted); err != nil {
		t.Fatal(err)
	}
	third, err := db.ClaimWantedAlbumForDownload(w.ID)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if !third {
		t.Error("claim should succeed again once reverted back to wanted")
	}
}

func TestClaimWantedAlbumForDownloadNonexistentRow(t *testing.T) {
	db := newTestStore(t)
	claimed, err := db.ClaimWantedAlbumForDownload(999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed {
		t.Error("claiming a nonexistent wanted album should never succeed")
	}
}
