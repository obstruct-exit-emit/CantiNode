package database

import (
	"testing"
	"time"
)

func TestGetOrCreateArtistCreatesThenReuses(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	a1, err := db.GetOrCreateArtist(ctx, "mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if a1.ID == 0 {
		t.Error("expected nonzero ID")
	}

	a2, err := db.GetOrCreateArtist(ctx, "mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist (second call): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}

	got, err := db.GetArtist(ctx, a1.ID)
	if err != nil {
		t.Fatalf("GetArtist: %v", err)
	}
	if got.Name != "Boards of Canada" {
		t.Errorf("Name = %q, want Boards of Canada", got.Name)
	}
}

func TestGetArtistNotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.GetArtist(t.Context(), 999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListArtistsEmptyIsNotNil guards against a real bug — see
// TestListRootFoldersEmptyIsNotNil's doc comment for the full story.
func TestListArtistsEmptyIsNotNil(t *testing.T) {
	db := openTestDB(t)
	list, err := db.ListArtists(t.Context())
	if err != nil {
		t.Fatalf("ListArtists: %v", err)
	}
	if list == nil {
		t.Error("ListArtists returned nil for an empty result, want a non-nil empty slice")
	}
}

// TestListArtistsIncludesMonitoredButUnowned guards the Phase 3 fix: an
// artist that's monitored but owns no track files yet must still show up
// (that's the whole point of unifying Library/Wanted into one artist
// list), while a plain "exists in `artists` but neither owns anything
// nor is monitored" row (shouldn't really happen, but defensively) stays
// excluded.
func TestListArtistsIncludesMonitoredButUnowned(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	monitored, err := db.GetOrCreateArtist(ctx, "monitored-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetArtistMonitored(ctx, monitored.ID, true); err != nil {
		t.Fatal(err)
	}

	if _, err := db.GetOrCreateArtist(ctx, "neither-mbid", "Nobody", "Nobody"); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListArtists(ctx)
	if err != nil {
		t.Fatalf("ListArtists: %v", err)
	}
	if len(list) != 1 || list[0].ID != monitored.ID {
		t.Errorf("list = %+v, want only the monitored artist", list)
	}
	if !list[0].IsMonitored {
		t.Error("IsMonitored should be true")
	}
}

func TestSetArtistMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	if a.IsMonitored {
		t.Error("a fresh artist should not start monitored")
	}

	if err := db.SetArtistMonitored(ctx, a.ID, true); err != nil {
		t.Fatalf("SetArtistMonitored(true): %v", err)
	}
	got, err := db.GetArtist(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsMonitored {
		t.Error("IsMonitored should be true")
	}

	if err := db.SetArtistMonitored(ctx, a.ID, false); err != nil {
		t.Fatalf("SetArtistMonitored(false): %v", err)
	}
	got, err = db.GetArtist(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsMonitored {
		t.Error("IsMonitored should be false after unmonitoring")
	}
}

func TestSetArtistSynced(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetArtistSynced(ctx, a.ID, now); err != nil {
		t.Fatalf("SetArtistSynced: %v", err)
	}
	got, err := db.GetArtist(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncedAt == nil {
		t.Fatal("LastSyncedAt should be set")
	}
}

func TestSetArtistMetadata(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetArtistMetadata(ctx, a.ID, "A great band.", "https://example.com/img.jpg", now); err != nil {
		t.Fatalf("SetArtistMetadata: %v", err)
	}
	got, err := db.GetArtist(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bio != "A great band." || got.ImageURL != "https://example.com/img.jpg" || got.MetadataFetchedAt == nil {
		t.Errorf("got = %+v", got)
	}
}

func TestReplaceAndListArtistReleaseGroups(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	a, err := db.GetOrCreateArtist(ctx, "mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}

	groups := []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Music Has the Right to Children", PrimaryType: "Album", FirstReleaseDate: "1998-04-20"},
		{ReleaseGroupMBID: "rg-2", Title: "Live at Warp", PrimaryType: "Album", SecondaryTypes: []string{"Live"}, FirstReleaseDate: "2001-01-01"},
	}
	if err := db.ReplaceArtistReleaseGroups(ctx, a.ID, groups); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}

	got, err := db.ListArtistReleaseGroups(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListArtistReleaseGroups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	byMBID := map[string]ReleaseGroupCache{}
	for _, g := range got {
		byMBID[g.ReleaseGroupMBID] = g
	}
	if len(byMBID["rg-2"].SecondaryTypes) != 1 || byMBID["rg-2"].SecondaryTypes[0] != "Live" {
		t.Errorf("rg-2 SecondaryTypes = %v, want [Live]", byMBID["rg-2"].SecondaryTypes)
	}
	if len(byMBID["rg-1"].SecondaryTypes) != 0 {
		t.Errorf("rg-1 SecondaryTypes = %v, want empty", byMBID["rg-1"].SecondaryTypes)
	}
	if byMBID["rg-1"].SecondaryTypes == nil {
		t.Error("rg-1 SecondaryTypes is nil, want a non-nil empty slice (marshals to `null` not `[]` in the /missing JSON response, which crashed the web UI's .includes() check on it)")
	}

	// A second call replaces wholesale rather than accumulating.
	if err := db.ReplaceArtistReleaseGroups(ctx, a.ID, groups[:1]); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups (second call): %v", err)
	}
	got, err = db.ListArtistReleaseGroups(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len(got) after replace = %d, want 1", len(got))
	}
}
