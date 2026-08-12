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
// (track_count=0, status=""). HasReleaseGroupVersions must NOT count that
// placeholder as "already cached" — otherwise the backfill sweep skips
// every artist that predates this feature forever, and the version
// dropdown shows just the one stale entry instead of the real list.
func TestHasReleaseGroupVersionsIgnoresMigratedPlaceholder(t *testing.T) {
	s := newTestStore(t)

	if err := s.ReplaceReleaseGroupVersions("rg-migrated", []ReleaseGroupVersion{
		{ReleaseGroupMBID: "rg-migrated", ReleaseMBID: "rel-old", Title: "Some Album", IsRepresentative: true},
	}); err != nil {
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
	if err := s.SetCachedTracklist("rel-1", "rg-1", `[]`); err != nil {
		t.Fatalf("seed rg-1 tracklist: %v", err)
	}
	if err := s.SetCachedTracklist("rel-2", "rg-2", `[]`); err != nil {
		t.Fatalf("seed rg-2 tracklist: %v", err)
	}

	if err := s.DeleteReleaseGroupCache([]string{"rg-1"}); err != nil {
		t.Fatalf("DeleteReleaseGroupCache: %v", err)
	}

	if versions, err := s.ListReleaseGroupVersions("rg-1"); err != nil || len(versions) != 0 {
		t.Errorf("rg-1 versions after delete = %+v, err %v, want empty", versions, err)
	}
	if _, err := s.GetCachedTracklist("rel-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rg-1 tracklist after delete: err = %v, want ErrNotFound", err)
	}

	if versions, err := s.ListReleaseGroupVersions("rg-2"); err != nil || len(versions) != 1 {
		t.Errorf("rg-2 versions after unrelated delete = %+v, err %v, want still 1", versions, err)
	}
	if _, err := s.GetCachedTracklist("rel-2"); err != nil {
		t.Errorf("rg-2 tracklist after unrelated delete: err = %v, want still cached", err)
	}
}

func TestDeleteReleaseGroupCacheEmptyInputIsNoop(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteReleaseGroupCache(nil); err != nil {
		t.Errorf("DeleteReleaseGroupCache(nil) = %v, want nil", err)
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
