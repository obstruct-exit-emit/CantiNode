package musiclibrary

import (
	"errors"
	"testing"
)

func TestReplaceAndListReleaseGroupVersions(t *testing.T) {
	s := newTestStore(t)

	versions := []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-official", Title: "The Wall", Status: "Official", ReleaseDate: "1979-11-30", TrackCount: 26, IsRepresentative: true},
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-remaster", Title: "The Wall (Remaster)", Status: "Official", ReleaseDate: "2011-01-01", TrackCount: 26},
	}
	if err := s.ReplaceReleaseGroupVersions("rg-1", versions); err != nil {
		t.Fatalf("ReplaceReleaseGroupVersions: %v", err)
	}

	got, err := s.ListReleaseGroupVersions("rg-1")
	if err != nil {
		t.Fatalf("ListReleaseGroupVersions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("versions = %+v, want 2", got)
	}
	if got[0].ReleaseMBID != "rel-official" || !got[0].IsRepresentative {
		t.Errorf("first version = %+v, want the representative one first", got[0])
	}

	has, err := s.HasReleaseGroupVersions("rg-1")
	if err != nil || !has {
		t.Errorf("HasReleaseGroupVersions(rg-1) = %v, %v, want true, nil", has, err)
	}
	has, err = s.HasReleaseGroupVersions("rg-unknown")
	if err != nil || has {
		t.Errorf("HasReleaseGroupVersions(rg-unknown) = %v, %v, want false, nil", has, err)
	}

	rep, err := s.GetRepresentativeReleaseVersion("rg-1")
	if err != nil {
		t.Fatalf("GetRepresentativeReleaseVersion: %v", err)
	}
	if rep.ReleaseMBID != "rel-official" {
		t.Errorf("representative = %q, want rel-official", rep.ReleaseMBID)
	}

	// A second Replace wholesale swaps the set, not merges it.
	if err := s.ReplaceReleaseGroupVersions("rg-1", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-only", Title: "The Wall", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("second ReplaceReleaseGroupVersions: %v", err)
	}
	got, err = s.ListReleaseGroupVersions("rg-1")
	if err != nil || len(got) != 1 || got[0].ReleaseMBID != "rel-only" {
		t.Fatalf("after replace, versions = %+v, err %v, want just rel-only", got, err)
	}
}

// TestHasReleaseGroupVersionsIgnoresMigratedPlaceholder is the regression
// test for a real bug found live in production: migration 022 carries over
// a release group's pre-existing single-tracklist-cache row as a
// release_group_versions row with only release_mbid/title populated
// (fetched=0, per migration 023's retroactive backfill). HasReleaseGroupVersions
// must NOT count that placeholder as "already cached" — otherwise the
// backfill sweep skips every artist that predates this feature forever, and
// the version dropdown shows just the one stale entry instead of the real
// list. ReplaceReleaseGroupVersions itself always marks fetched=1 (every
// call represents genuinely-fetched data), so the placeholder here is seeded
// directly via SQL to simulate what migration 022+023 actually leave behind.
func TestHasReleaseGroupVersionsIgnoresMigratedPlaceholder(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(
		`INSERT INTO release_group_versions (release_group_mbid, release_mbid, title, is_representative, fetched)
		 VALUES (?, ?, ?, 1, 0)`,
		"rg-migrated", "rel-old", "Some Album"); err != nil {
		t.Fatalf("seed migrated placeholder: %v", err)
	}

	has, err := s.HasReleaseGroupVersions("rg-migrated")
	if err != nil || has {
		t.Errorf("HasReleaseGroupVersions(rg-migrated) = %v, %v, want false (placeholder shouldn't count)", has, err)
	}

	// A genuinely fetched single-version release group (some albums really
	// do only have one known release) must still count as cached.
	if err := s.ReplaceReleaseGroupVersions("rg-real", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-real", ReleaseMBID: "rel-real", Title: "Some Album", Status: "Official", TrackCount: 10, IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed real version: %v", err)
	}
	has, err = s.HasReleaseGroupVersions("rg-real")
	if err != nil || !has {
		t.Errorf("HasReleaseGroupVersions(rg-real) = %v, %v, want true", has, err)
	}
}

// TestHasReleaseGroupVersionsCountsGenuinelySparseRealData is the case that
// motivated replacing the old field-value heuristic
// (track_count > 0 || status != "") with an explicit fetched column: a real
// MusicBrainz release whose browse response happens to have neither field
// populated used to be misclassified as a migration placeholder forever,
// triggering an unbounded live re-fetch on every scan. With the explicit
// flag, ReplaceReleaseGroupVersions marks it fetched=1 regardless of how
// sparse the fetched data itself is.
func TestHasReleaseGroupVersionsCountsGenuinelySparseRealData(t *testing.T) {
	s := newTestStore(t)

	if err := s.ReplaceReleaseGroupVersions("rg-sparse", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-sparse", ReleaseMBID: "rel-sparse", Title: "Some Album", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed sparse real version: %v", err)
	}

	has, err := s.HasReleaseGroupVersions("rg-sparse")
	if err != nil || !has {
		t.Errorf("HasReleaseGroupVersions(rg-sparse) = %v, %v, want true (genuinely fetched, just sparse)", has, err)
	}
}

func TestGetRepresentativeReleaseVersionNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetRepresentativeReleaseVersion("rg-unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestCachedTracklistPerRelease confirms two different versions of the
// same release group get independent cached tracklists (the whole point
// of keying release_tracklist_cache by release_mbid, not release_group_mbid
// as the old single-release scheme did).
func TestCachedTracklistPerRelease(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetCachedTracklist("rel-official", "rg-1", `["track A"]`); err != nil {
		t.Fatalf("SetCachedTracklist official: %v", err)
	}
	if err := s.SetCachedTracklist("rel-remaster", "rg-1", `["track B"]`); err != nil {
		t.Fatalf("SetCachedTracklist remaster: %v", err)
	}

	official, err := s.GetCachedTracklist("rel-official")
	if err != nil {
		t.Fatalf("GetCachedTracklist official: %v", err)
	}
	if official.TracksJSON != `["track A"]` || official.ReleaseGroupMBID != "rg-1" {
		t.Errorf("official = %+v, want track A / rg-1", official)
	}

	remaster, err := s.GetCachedTracklist("rel-remaster")
	if err != nil {
		t.Fatalf("GetCachedTracklist remaster: %v", err)
	}
	if remaster.TracksJSON != `["track B"]` {
		t.Errorf("remaster = %+v, want track B", remaster)
	}

	if _, err := s.GetCachedTracklist("rel-unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestDeleteReleaseGroupCachePurgesVersionsAndTracklist is the regression
// test for the "removing an artist should delete its cached metadata"
// requirement: release_group_versions and release_tracklist_cache rows for
// a given release group must both actually disappear, and an unrelated
// release group's rows must survive untouched.
func TestDeleteReleaseGroupCachePurgesVersionsAndTracklist(t *testing.T) {
	s := newTestStore(t)

	if err := s.ReplaceReleaseGroupVersions("rg-1", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-1", Title: "Album One", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed rg-1 versions: %v", err)
	}
	if err := s.ReplaceReleaseGroupVersions("rg-2", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-2", ReleaseMBID: "rel-2", Title: "Album Two", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed rg-2 versions: %v", err)
	}
	if err := s.ReplaceReleaseGroupVersions("rg-3", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-3", ReleaseMBID: "rel-3", Title: "Album Three", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed rg-3 versions: %v", err)
	}
	if err := s.SetCachedTracklist("rel-1", "rg-1", `[]`); err != nil {
		t.Fatalf("seed rg-1 tracklist: %v", err)
	}
	if err := s.SetCachedTracklist("rel-2", "rg-2", `[]`); err != nil {
		t.Fatalf("seed rg-2 tracklist: %v", err)
	}
	if err := s.SetCachedTracklist("rel-3", "rg-3", `[]`); err != nil {
		t.Fatalf("seed rg-3 tracklist: %v", err)
	}

	// Two release groups purged in the same call — the batched IN-clause
	// delete must cover every id passed, not just the first.
	if err := s.DeleteReleaseGroupCache([]string{"rg-1", "rg-3"}); err != nil {
		t.Fatalf("DeleteReleaseGroupCache: %v", err)
	}

	if versions, err := s.ListReleaseGroupVersions("rg-1"); err != nil || len(versions) != 0 {
		t.Errorf("rg-1 versions after delete = %+v, err %v, want empty", versions, err)
	}
	if _, err := s.GetCachedTracklist("rel-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rg-1 tracklist after delete: err = %v, want ErrNotFound", err)
	}
	if versions, err := s.ListReleaseGroupVersions("rg-3"); err != nil || len(versions) != 0 {
		t.Errorf("rg-3 versions after delete = %+v, err %v, want empty", versions, err)
	}
	if _, err := s.GetCachedTracklist("rel-3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rg-3 tracklist after delete: err = %v, want ErrNotFound", err)
	}

	if versions, err := s.ListReleaseGroupVersions("rg-2"); err != nil || len(versions) != 1 {
		t.Errorf("rg-2 versions after unrelated delete = %+v, err %v, want still 1", versions, err)
	}
	if _, err := s.GetCachedTracklist("rel-2"); err != nil {
		t.Errorf("rg-2 tracklist after unrelated delete: err = %v, want still cached", err)
	}
}

// TestReleaseGroupMBIDsStillReferenced confirms it correctly distinguishes
// a release group no artist references anymore from one another artist
// still does — the check internal/api's purgeArtistCaches relies on to
// avoid wiping a still-needed artist's cached metadata when a different
// artist sharing the same release group is removed.
func TestReleaseGroupMBIDsStillReferenced(t *testing.T) {
	s := newTestStore(t)

	artistA, err := s.GetOrCreateArtist("artist-a", "Artist A", "Artist A")
	if err != nil {
		t.Fatalf("GetOrCreateArtist A: %v", err)
	}
	artistB, err := s.GetOrCreateArtist("artist-b", "Artist B", "Artist B")
	if err != nil {
		t.Fatalf("GetOrCreateArtist B: %v", err)
	}
	if err := s.ReplaceArtistReleaseGroups(artistA.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-shared", Title: "Split Release"},
		{ReleaseGroupMBID: "rg-a-only", Title: "Artist A Solo"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups A: %v", err)
	}
	if err := s.ReplaceArtistReleaseGroups(artistB.ID, []ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-shared", Title: "Split Release"},
	}); err != nil {
		t.Fatalf("ReplaceArtistReleaseGroups B: %v", err)
	}

	got, err := s.ReleaseGroupMBIDsStillReferenced([]string{"rg-shared", "rg-a-only", "rg-unreferenced"})
	if err != nil {
		t.Fatalf("ReleaseGroupMBIDsStillReferenced: %v", err)
	}
	if !got["rg-shared"] {
		t.Error("rg-shared should be referenced (both artists)")
	}
	if !got["rg-a-only"] {
		t.Error("rg-a-only should be referenced (artist A)")
	}
	if got["rg-unreferenced"] {
		t.Error("rg-unreferenced should not be referenced by anyone")
	}

	// After removing artist A's own rows (simulating DeleteArtist's cascade
	// for just that artist), rg-shared must still show as referenced
	// (artist B), but rg-a-only must not (nobody references it anymore).
	if err := s.ReplaceArtistReleaseGroups(artistA.ID, nil); err != nil {
		t.Fatalf("clear artist A release groups: %v", err)
	}
	got, err = s.ReleaseGroupMBIDsStillReferenced([]string{"rg-shared", "rg-a-only"})
	if err != nil {
		t.Fatalf("ReleaseGroupMBIDsStillReferenced after clearing A: %v", err)
	}
	if !got["rg-shared"] {
		t.Error("rg-shared should still be referenced (artist B)")
	}
	if got["rg-a-only"] {
		t.Error("rg-a-only should no longer be referenced (artist A's rows cleared)")
	}
}

func TestReleaseGroupMBIDsStillReferencedEmptyInput(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ReleaseGroupMBIDsStillReferenced(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ReleaseGroupMBIDsStillReferenced(nil) = %v, %v, want empty map, nil", got, err)
	}
}

// TestReleaseGroupMBIDsWithRealVersions is the bulk counterpart to
// TestHasReleaseGroupVersionsIgnoresMigratedPlaceholder — confirms the
// batched form used by the backfill sweep applies the same
// placeholder-vs-real distinction as the single-release-group check.
func TestReleaseGroupMBIDsWithRealVersions(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.db.Exec(
		`INSERT INTO release_group_versions (release_group_mbid, release_mbid, title, is_representative, fetched)
		 VALUES (?, ?, ?, 1, 0)`,
		"rg-placeholder", "rel-old", "Some Album"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceReleaseGroupVersions("rg-real", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-real", ReleaseMBID: "rel-real", Title: "Some Album", Status: "Official", TrackCount: 10, IsRepresentative: true},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ReleaseGroupMBIDsWithRealVersions([]string{"rg-placeholder", "rg-real", "rg-never-cached"})
	if err != nil {
		t.Fatalf("ReleaseGroupMBIDsWithRealVersions: %v", err)
	}
	if got["rg-placeholder"] {
		t.Error("rg-placeholder should not count as having real versions")
	}
	if !got["rg-real"] {
		t.Error("rg-real should count as having real versions")
	}
	if got["rg-never-cached"] {
		t.Error("rg-never-cached should not count as having real versions")
	}
}

func TestReleaseGroupMBIDsWithRealVersionsEmptyInput(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ReleaseGroupMBIDsWithRealVersions(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ReleaseGroupMBIDsWithRealVersions(nil) = %v, %v, want empty map, nil", got, err)
	}
}

func TestDeleteReleaseGroupCacheEmptyInputIsNoop(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteReleaseGroupCache(nil); err != nil {
		t.Errorf("DeleteReleaseGroupCache(nil) = %v, want nil", err)
	}
}

// TestListReleaseGroupVersionsBulk is the bulk counterpart to
// TestReplaceAndListReleaseGroupVersions — confirms one call can collect
// every requested release group's versions at once (used by internal/api's
// purgeArtistCaches to avoid one query per release group when purging an
// artist's whole discography).
func TestListReleaseGroupVersionsBulk(t *testing.T) {
	s := newTestStore(t)

	if err := s.ReplaceReleaseGroupVersions("rg-1", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-1a", Title: "Album One", IsRepresentative: true},
		{ReleaseGroupMBID: "rg-1", ReleaseMBID: "rel-1b", Title: "Album One (Remaster)"},
	}); err != nil {
		t.Fatalf("seed rg-1: %v", err)
	}
	if err := s.ReplaceReleaseGroupVersions("rg-2", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-2", ReleaseMBID: "rel-2", Title: "Album Two", IsRepresentative: true},
	}); err != nil {
		t.Fatalf("seed rg-2: %v", err)
	}
	// rg-3 is deliberately never seeded — absent from the result, not an
	// error, same as a single-group ListReleaseGroupVersions miss.

	got, err := s.ListReleaseGroupVersionsBulk([]string{"rg-1", "rg-2", "rg-3"})
	if err != nil {
		t.Fatalf("ListReleaseGroupVersionsBulk: %v", err)
	}
	if len(got["rg-1"]) != 2 {
		t.Errorf("rg-1 versions = %+v, want 2", got["rg-1"])
	}
	if len(got["rg-2"]) != 1 {
		t.Errorf("rg-2 versions = %+v, want 1", got["rg-2"])
	}
	if _, ok := got["rg-3"]; ok {
		t.Errorf("rg-3 = %+v, want absent (never cached)", got["rg-3"])
	}
}

func TestListReleaseGroupVersionsBulkEmptyInput(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ListReleaseGroupVersionsBulk(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ListReleaseGroupVersionsBulk(nil) = %v, %v, want empty map, nil", got, err)
	}
}

func TestSetArtistMusicBrainzMetadataRoundTrip(t *testing.T) {
	s := newTestStore(t)

	a, err := s.GetOrCreateArtist("artist-mbid", "Test Artist", "Test Artist")
	if err != nil {
		t.Fatalf("GetOrCreateArtist: %v", err)
	}
	if len(a.Genres) != 0 || len(a.Tags) != 0 {
		t.Errorf("new artist Genres/Tags = %+v/%+v, want both empty", a.Genres, a.Tags)
	}

	if err := s.SetArtistMusicBrainzMetadata(a.ID, []string{"idm", "electronic"}, []string{"scottish"}, 4.5, 42); err != nil {
		t.Fatalf("SetArtistMusicBrainzMetadata: %v", err)
	}

	got, err := s.GetArtist(a.ID)
	if err != nil {
		t.Fatalf("GetArtist: %v", err)
	}
	if len(got.Genres) != 2 || got.Genres[0] != "idm" || got.Genres[1] != "electronic" {
		t.Errorf("Genres = %+v, want [idm electronic]", got.Genres)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "scottish" {
		t.Errorf("Tags = %+v, want [scottish]", got.Tags)
	}
	if got.RatingValue != 4.5 || got.RatingVotes != 42 {
		t.Errorf("Rating = %v/%v, want 4.5/42", got.RatingValue, got.RatingVotes)
	}
}
