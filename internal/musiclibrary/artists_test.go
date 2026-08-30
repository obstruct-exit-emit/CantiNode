package musiclibrary

import (
	"testing"
	"time"
)

func TestSearchRelevanceName(t *testing.T) {
	real := Artist{Name: "Avantasia", Kind: "artist"}
	if got := real.SearchRelevanceName(); got != "Avantasia" {
		t.Errorf("real artist SearchRelevanceName() = %q, want Avantasia", got)
	}
}

func TestGetOrCreateArtistCreatesThenReuses(t *testing.T) {
	db := newTestStore(t)

	a1, err := db.GetOrCreateArtist("mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if a1.ID == 0 {
		t.Error("expected nonzero ID")
	}

	a2, err := db.GetOrCreateArtist("mbid-1", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatalf("GetOrCreateArtist (second call): %v", err)
	}
	if a2.ID != a1.ID {
		t.Errorf("second call created a new row: ID = %d, want %d", a2.ID, a1.ID)
	}

	got, err := db.GetArtist(a1.ID)
	if err != nil {
		t.Fatalf("GetArtist: %v", err)
	}
	if got.Name != "Boards of Canada" {
		t.Errorf("Name = %q, want Boards of Canada", got.Name)
	}
}

// TestDeleteArtistCascadesAlbumsTracksWanted proves DeleteArtist's own FK
// cascade (albums -> tracks, wanted_albums, artist_release_groups) works
// as the schema promises. It deliberately does NOT own any track_files —
// RemoveArtist (internal/acquisition) is responsible for detaching those
// first; a raw DeleteArtist call against a track_files-owning artist is
// exactly the orphaning bug DeleteArtist's own doc comment warns about,
// not something this test exercises.
func TestDeleteArtistCascadesAlbumsTracksWanted(t *testing.T) {
	db := newTestStore(t)

	artist, err := db.GetOrCreateArtist("a-mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(artist.ID, "al-mbid", "rg-mbid", "Album", "2020", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := db.GetOrCreateTrack(album.ID, "t-mbid", "Song", 1, 1, 1000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceArtistReleaseGroups(artist.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-mbid", Title: "Album", PrimaryType: "Album"},
	}); err != nil {
		t.Fatal(err)
	}
	wanted, err := db.GetOrCreateWantedAlbum(artist.ID, "rg-2", "Other Album", "Album", "2021")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteArtist(artist.ID); err != nil {
		t.Fatalf("DeleteArtist: %v", err)
	}

	if _, err := db.GetArtist(artist.ID); err != ErrNotFound {
		t.Errorf("GetArtist after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetAlbum(album.ID); err != ErrNotFound {
		t.Errorf("GetAlbum after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetTrack(track.ID); err != ErrNotFound {
		t.Errorf("GetTrack after delete: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetWantedAlbum(wanted.ID); err != ErrNotFound {
		t.Errorf("GetWantedAlbum after delete: err = %v, want ErrNotFound", err)
	}
	groups, err := db.ListArtistReleaseGroups(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("release groups after delete = %+v, want empty", groups)
	}
}

func TestGetArtistNotFound(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.GetArtist(999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestListArtistsEmptyIsNotNil guards against a real bug: a nil slice
// marshals to JSON null, not [], which would crash a web UI doing
// .length/.map() on an empty result.
func TestListArtistsEmptyIsNotNil(t *testing.T) {
	db := newTestStore(t)
	list, err := db.ListArtists()
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
	db := newTestStore(t)

	monitored, err := db.GetOrCreateArtist("monitored-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetArtistMonitored(monitored.ID, true); err != nil {
		t.Fatal(err)
	}

	if _, err := db.GetOrCreateArtist("neither-mbid", "Nobody", "Nobody"); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListArtists()
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
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}
	if a.IsMonitored {
		t.Error("a fresh artist should not start monitored")
	}

	if err := db.SetArtistMonitored(a.ID, true); err != nil {
		t.Fatalf("SetArtistMonitored(true): %v", err)
	}
	got, err := db.GetArtist(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsMonitored {
		t.Error("IsMonitored should be true")
	}

	if err := db.SetArtistMonitored(a.ID, false); err != nil {
		t.Fatalf("SetArtistMonitored(false): %v", err)
	}
	got, err = db.GetArtist(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsMonitored {
		t.Error("IsMonitored should be false after unmonitoring")
	}
}

func TestSetArtistSynced(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetArtistSynced(a.ID, now); err != nil {
		t.Fatalf("SetArtistSynced: %v", err)
	}
	got, err := db.GetArtist(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSyncedAt == nil {
		t.Fatal("LastSyncedAt should be set")
	}
}

func TestSetArtistMetadata(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("mbid", "Artist", "Artist")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.SetArtistMetadata(a.ID, "A great band.", "https://example.com/img.jpg", now); err != nil {
		t.Fatalf("SetArtistMetadata: %v", err)
	}
	got, err := db.GetArtist(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bio != "A great band." || got.ImageURL != "https://example.com/img.jpg" || got.MetadataFetchedAt == nil {
		t.Errorf("got = %+v", got)
	}
}

func TestReplaceAndListArtistReleaseGroups(t *testing.T) {
	db := newTestStore(t)
	a, err := db.GetOrCreateArtist("mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}

	groups := []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-1", Title: "Music Has the Right to Children", PrimaryType: "Album", FirstReleaseDate: "1998-04-20"},
		{ReleaseGroupMBID: "rg-2", Title: "Live at Warp", PrimaryType: "Album", SecondaryTypes: []string{"Live"}, FirstReleaseDate: "2001-01-01"},
	}
	if err := db.ReplaceArtistReleaseGroups(a.ID, groups); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}

	got, err := db.ListArtistReleaseGroups(a.ID)
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
	if err := db.ReplaceArtistReleaseGroups(a.ID, groups[:1]); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups (second call): %v", err)
	}
	got, err = db.ListArtistReleaseGroups(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("len(got) after replace = %d, want 1", len(got))
	}
}

// TestListUpcomingReleasesFiltersAndAnnotates covers the release Calendar's
// own query: monitored-only, date-windowed, already-owned releases
// excluded, and a wanted release group carries its wanted status along so
// the calendar can show it differently from one nobody's searching for yet.
func TestListUpcomingReleasesFiltersAndAnnotates(t *testing.T) {
	db := newTestStore(t)

	monitored, err := db.GetOrCreateArtist("mbid-mon", "Monitored Artist", "Monitored Artist")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetArtistMonitored(monitored.ID, true); err != nil {
		t.Fatal(err)
	}
	unmonitored, err := db.GetOrCreateArtist("mbid-unmon", "Unmonitored Artist", "Unmonitored Artist")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.ReplaceArtistReleaseGroups(monitored.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-in-window", Title: "In Window", PrimaryType: "Album", FirstReleaseDate: "2026-09-15"},
		{ReleaseGroupMBID: "rg-too-late", Title: "Too Late", PrimaryType: "Album", FirstReleaseDate: "2027-01-01"},
		{ReleaseGroupMBID: "rg-owned", Title: "Already Owned", PrimaryType: "Album", FirstReleaseDate: "2026-09-16"},
		{ReleaseGroupMBID: "rg-wanted", Title: "Wanted One", PrimaryType: "Album", FirstReleaseDate: "2026-09-17"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups: %v", err)
	}
	if err := db.ReplaceArtistReleaseGroups(unmonitored.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-unmon", Title: "Should Not Appear", PrimaryType: "Album", FirstReleaseDate: "2026-09-15"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups (unmonitored): %v", err)
	}

	if _, err := db.GetOrCreateAlbum(monitored.ID, "release-mbid", "rg-owned", "Already Owned", "2026-09-16", "Album"); err != nil {
		t.Fatalf("GetOrCreateAlbum: %v", err)
	}
	wanted, err := db.GetOrCreateWantedAlbum(monitored.ID, "rg-wanted", "Wanted One", "Album", "2026-09-17")
	if err != nil {
		t.Fatalf("GetOrCreateWantedAlbum: %v", err)
	}

	got, err := db.ListUpcomingReleases("2026-09-01", "2026-09-30")
	if err != nil {
		t.Fatalf("ListUpcomingReleases: %v", err)
	}
	byMBID := map[string]CalendarEntry{}
	for _, e := range got {
		byMBID[e.ReleaseGroupMBID] = e
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (rg-in-window, rg-wanted); got %+v", len(got), got)
	}
	if _, ok := byMBID["rg-too-late"]; ok {
		t.Error("rg-too-late appeared — outside the date window")
	}
	if _, ok := byMBID["rg-owned"]; ok {
		t.Error("rg-owned appeared — already an owned album")
	}
	if _, ok := byMBID["rg-unmon"]; ok {
		t.Error("rg-unmon appeared — its artist isn't monitored")
	}
	inWindow, ok := byMBID["rg-in-window"]
	if !ok {
		t.Fatal("rg-in-window missing")
	}
	if inWindow.WantedAlbumID != 0 {
		t.Errorf("rg-in-window WantedAlbumID = %d, want 0 (never marked wanted)", inWindow.WantedAlbumID)
	}
	if inWindow.ArtistName != "Monitored Artist" {
		t.Errorf("rg-in-window ArtistName = %q, want %q", inWindow.ArtistName, "Monitored Artist")
	}
	wantedEntry, ok := byMBID["rg-wanted"]
	if !ok {
		t.Fatal("rg-wanted missing")
	}
	if wantedEntry.WantedAlbumID != wanted.ID || wantedEntry.WantedStatus != "wanted" {
		t.Errorf("rg-wanted = %+v, want WantedAlbumID=%d WantedStatus=wanted", wantedEntry, wanted.ID)
	}
}

func TestGetOrCreateArtistDefaultsToArtistKind(t *testing.T) {
	db := newTestStore(t)

	a, err := db.GetOrCreateArtist("a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "artist" {
		t.Errorf("Kind = %q, want artist", a.Kind)
	}
	got, err := db.GetArtist(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "artist" {
		t.Errorf("GetArtist Kind = %q, want artist", got.Kind)
	}
}
