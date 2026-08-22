package musicbrainz

import "testing"

// TestBestReleasePrefersCleanAlbumOverCompilation is the regression test
// for the "Crossroads" misattribution: a Derek and the Dominos recording
// ("Tell the Truth", single version) is genuinely linked to both the
// Eric Clapton "Crossroads" box set (release-group secondary-type
// "Compilation") and the actual "Layla and Other Assorted Love Songs"
// studio album — with Crossroads listed first in MusicBrainz's own
// Releases order. Without this preference, BestRelease("") used to just
// take Releases[0] and resolve the track to the wrong album entirely.
func TestBestReleasePrefersCleanAlbumOverCompilation(t *testing.T) {
	rec := Recording{
		ID: "tell-the-truth",
		Releases: []Release{
			{
				ID:    "crossroads-release",
				Title: "Crossroads",
				ReleaseGroup: ReleaseGroup{
					ID: "rg-crossroads", Title: "Crossroads",
					PrimaryType: "Album", SecondaryTypes: []string{"Compilation"},
				},
			},
			{
				ID:    "layla-release",
				Title: "Layla and Other Assorted Love Songs",
				ReleaseGroup: ReleaseGroup{
					ID: "rg-layla", Title: "Layla and Other Assorted Love Songs",
					PrimaryType: "Album",
				},
			},
		},
	}

	got := rec.BestRelease("")
	if got.ID != "layla-release" {
		t.Errorf("BestRelease(\"\").ID = %q, want %q (the clean studio album, not the compilation listed first)", got.ID, "layla-release")
	}
}

// TestBestReleasePrefersCleanAlbumOverLive is the same shape of bug as
// TestBestReleasePrefersCleanAlbumOverCompilation, but for a "Live"
// secondary type (e.g. "Fillmore Double Night") instead of "Compilation"
// — the heuristic is about SecondaryTypes generally, not one hardcoded
// value.
func TestBestReleasePrefersCleanAlbumOverLive(t *testing.T) {
	rec := Recording{
		ID: "tell-the-truth",
		Releases: []Release{
			{
				ID:    "live-bootleg-release",
				Title: "Fillmore Double Night",
				ReleaseGroup: ReleaseGroup{
					ID: "rg-fillmore", Title: "Fillmore Double Night",
					PrimaryType: "Album", SecondaryTypes: []string{"Live"},
				},
			},
			{
				ID:    "layla-release",
				Title: "Layla and Other Assorted Love Songs",
				ReleaseGroup: ReleaseGroup{
					ID: "rg-layla", Title: "Layla and Other Assorted Love Songs",
					PrimaryType: "Album",
				},
			},
		},
	}

	got := rec.BestRelease("")
	if got.ID != "layla-release" {
		t.Errorf("BestRelease(\"\").ID = %q, want %q", got.ID, "layla-release")
	}
}

// TestBestReleasePreferredMBIDStillWinsOverCleanAlbum confirms an
// explicit preferredReleaseMBID (a file's own embedded release tag) still
// overrides the clean-album heuristic — the heuristic only applies when
// CantiNode has no better signal of its own.
func TestBestReleasePreferredMBIDStillWinsOverCleanAlbum(t *testing.T) {
	rec := Recording{
		Releases: []Release{
			{ID: "compilation-release", ReleaseGroup: ReleaseGroup{PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}}},
			{ID: "clean-album-release", ReleaseGroup: ReleaseGroup{PrimaryType: "Album"}},
		},
	}

	got := rec.BestRelease("compilation-release")
	if got.ID != "compilation-release" {
		t.Errorf("BestRelease(preferred) = %q, want the explicitly preferred release even though it's not a clean album", got.ID)
	}
}

// TestBestReleaseFallsBackWhenNoCleanAlbumExists confirms a recording that
// genuinely only exists on compilations/live releases still resolves to
// something, rather than BestRelease returning a zero Release just
// because none of the candidates are "clean".
func TestBestReleaseFallsBackWhenNoCleanAlbumExists(t *testing.T) {
	rec := Recording{
		Releases: []Release{
			{ID: "only-compilation", ReleaseGroup: ReleaseGroup{PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}}},
		},
	}

	got := rec.BestRelease("")
	if got.ID != "only-compilation" {
		t.Errorf("BestRelease(\"\") = %q, want the only available release even though it's not clean", got.ID)
	}
}

func TestBestReleaseNoReleasesReturnsZeroValue(t *testing.T) {
	rec := Recording{}
	got := rec.BestRelease("")
	if got.ID != "" {
		t.Errorf("BestRelease(\"\") on a recording with no releases = %+v, want a zero Release", got)
	}
}

// TestRecordingComposerResolvesWorkLevelArtistRel mirrors the real shape
// confirmed live against MusicBrainz for Jeff Buckley's "Hallelujah": the
// recording links a Work via a "performance" relation, and the composer
// credit lives on THAT Work's own relations, not the recording's — a
// "lyricist" relation on the same work must not be picked up alongside
// "composer".
func TestRecordingComposerResolvesWorkLevelArtistRel(t *testing.T) {
	rec := Recording{
		Relations: []Relation{
			{
				Type:       "performance",
				TargetType: "work",
				Work: &Work{
					ID:    "work-1",
					Title: "Hallelujah",
					Relations: []Relation{
						{Type: "composer", TargetType: "artist", Artist: &ArtistRef{ID: "cohen", Name: "Leonard Cohen"}},
						{Type: "lyricist", TargetType: "artist", Artist: &ArtistRef{ID: "cohen", Name: "Leonard Cohen"}},
						{Type: "arrangement", TargetType: "work", Work: &Work{ID: "arr-work"}},
					},
				},
			},
		},
	}

	got := rec.Composer()
	if got != "Leonard Cohen" {
		t.Errorf("Composer() = %q, want %q", got, "Leonard Cohen")
	}
}

// TestRecordingComposerDeduplicatesByArtistID confirms a work crediting the
// same artist under both "composer" and "writer" (or any other combination
// of the two recognized types) produces one name, not a repeated one.
func TestRecordingComposerDeduplicatesByArtistID(t *testing.T) {
	rec := Recording{
		Relations: []Relation{
			{
				Type: "performance", TargetType: "work",
				Work: &Work{Relations: []Relation{
					{Type: "composer", TargetType: "artist", Artist: &ArtistRef{ID: "a1", Name: "Same Artist"}},
					{Type: "writer", TargetType: "artist", Artist: &ArtistRef{ID: "a1", Name: "Same Artist"}},
				}},
			},
		},
	}

	got := rec.Composer()
	if got != "Same Artist" {
		t.Errorf("Composer() = %q, want %q (deduplicated)", got, "Same Artist")
	}
}

// TestRecordingComposerJoinsMultipleWriters confirms more than one distinct
// writer on a work is joined, in API order.
func TestRecordingComposerJoinsMultipleWriters(t *testing.T) {
	rec := Recording{
		Relations: []Relation{
			{
				Type: "performance", TargetType: "work",
				Work: &Work{Relations: []Relation{
					{Type: "writer", TargetType: "artist", Artist: &ArtistRef{ID: "a1", Name: "First Writer"}},
					{Type: "writer", TargetType: "artist", Artist: &ArtistRef{ID: "a2", Name: "Second Writer"}},
				}},
			},
		},
	}

	got := rec.Composer()
	if got != "First Writer; Second Writer" {
		t.Errorf("Composer() = %q, want %q", got, "First Writer; Second Writer")
	}
}

// TestRecordingComposerEmptyWhenRelationsUnpopulated confirms a Recording
// sourced from an endpoint that never returns relationship data (e.g.
// BatchLookupRecordings/SearchRecordings, both search-endpoint-backed —
// confirmed live to omit "relations" entirely regardless of inc params)
// simply yields an empty Composer rather than panicking on nil Relations.
func TestRecordingComposerEmptyWhenRelationsUnpopulated(t *testing.T) {
	rec := Recording{ID: "rec-1"}
	if got := rec.Composer(); got != "" {
		t.Errorf("Composer() = %q, want empty string", got)
	}
}
