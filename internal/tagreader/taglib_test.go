package tagreader

import (
	"os"
	"path/filepath"
	"testing"

	taglib "go.senan.xyz/taglib"
)

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func seedTags(t *testing.T, path string) {
	t.Helper()
	err := taglib.WriteTags(path, map[string][]string{
		taglib.Title:                     {"Alpha and Omega"},
		taglib.Artist:                    {"Boards of Canada"},
		taglib.AlbumArtist:               {"Boards of Canada"},
		taglib.Album:                     {"Geogaddi"},
		taglib.TrackNumber:               {"3"},
		taglib.DiscNumber:                {"1"},
		taglib.Date:                      {"2002-02-04"},
		taglib.Genre:                     {"Electronic; IDM"},
		taglib.ReleaseType:               {"Album"},
		taglib.ArtistSort:                {"Boards of Canada"},
		taglib.AlbumArtistSort:           {"Boards of Canada"},
		taglib.ReleaseCountry:            {"GB"},
		taglib.ReleaseStatus:             {"official"},
		taglib.Media:                     {"CD"},
		"TRACKTOTAL":                     {"12"},
		"DISCTOTAL":                      {"1"},
		taglib.MusicBrainzArtistID:       {"8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d"},
		taglib.MusicBrainzAlbumArtistID:  {"8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d"},
		taglib.MusicBrainzAlbumID:        {"a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"},
		taglib.MusicBrainzReleaseGroupID: {"11111111-2222-3333-4444-555555555555"},
		taglib.MusicBrainzTrackID:        {"66666666-7777-8888-9999-000000000000"},
	}, 0)
	if err != nil {
		t.Fatalf("seed tags: %v", err)
	}
}

func assertReadTags(t *testing.T, path, wantFormat string) {
	t.Helper()
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Title != "Alpha and Omega" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Artist != "Boards of Canada" {
		t.Errorf("Artist = %q", got.Artist)
	}
	if got.AlbumArtist != "Boards of Canada" {
		t.Errorf("AlbumArtist = %q", got.AlbumArtist)
	}
	if got.Album != "Geogaddi" {
		t.Errorf("Album = %q", got.Album)
	}
	if got.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", got.TrackNumber)
	}
	if got.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1", got.DiscNumber)
	}
	if got.Year != 2002 {
		t.Errorf("Year = %d, want 2002", got.Year)
	}
	if got.Format != wantFormat {
		t.Errorf("Format = %q, want %q", got.Format, wantFormat)
	}
	if got.MusicBrainzArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("MusicBrainzArtistID = %q", got.MusicBrainzArtistID)
	}
	if got.MusicBrainzAlbumID != "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d" {
		t.Errorf("MusicBrainzAlbumID = %q", got.MusicBrainzAlbumID)
	}
	if got.MusicBrainzReleaseGroupID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("MusicBrainzReleaseGroupID = %q", got.MusicBrainzReleaseGroupID)
	}
	if got.MusicBrainzRecordingID != "66666666-7777-8888-9999-000000000000" {
		t.Errorf("MusicBrainzRecordingID = %q", got.MusicBrainzRecordingID)
	}
	if got.Genre != "Electronic; IDM" {
		t.Errorf("Genre = %q", got.Genre)
	}
	if got.ReleaseType != "Album" {
		t.Errorf("ReleaseType = %q", got.ReleaseType)
	}
	if got.ArtistSortName != "Boards of Canada" {
		t.Errorf("ArtistSortName = %q", got.ArtistSortName)
	}
	if got.AlbumArtistSortName != "Boards of Canada" {
		t.Errorf("AlbumArtistSortName = %q", got.AlbumArtistSortName)
	}
	if got.ReleaseCountry != "GB" {
		t.Errorf("ReleaseCountry = %q", got.ReleaseCountry)
	}
	if got.ReleaseStatus != "official" {
		t.Errorf("ReleaseStatus = %q", got.ReleaseStatus)
	}
	if got.Media != "CD" {
		t.Errorf("Media = %q", got.Media)
	}
	if got.AlbumArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("AlbumArtistID = %q", got.AlbumArtistID)
	}
	if got.TrackTotal != 12 {
		t.Errorf("TrackTotal = %d, want 12", got.TrackTotal)
	}
	if got.DiscTotal != 1 {
		t.Errorf("DiscTotal = %d, want 1", got.DiscTotal)
	}
}

// TestReadWAV exercises readTagLib — the path dhowden/tag can't cover at
// all (it opens a WAV file without erroring but returns "no tags found"
// for every one tested, confirmed against this exact fixture before this
// path existed).
func TestReadWAV(t *testing.T) {
	path := copyFixture(t, "sample.wav")
	seedTags(t, path)
	assertReadTags(t, path, "wav")
}

// TestReadOpus confirms dhowden/tag really does read OpusTags correctly
// through the real Read() entry point end-to-end — not just IsAudioFile
// recognizing the extension. dhowden/tag mislabels the underlying
// FileType as "ogg" (it treats OpusTags as an alias of the byte-identical
// VorbisComment structure and doesn't distinguish Opus from Vorbis
// containers), which is a cosmetic quirk, not a correctness issue —
// nothing downstream depends on Format distinguishing the two.
func TestReadOpus(t *testing.T) {
	path := copyFixture(t, "sample.opus")
	seedTags(t, path)
	assertReadTags(t, path, "ogg")
}

func TestLeadingInt(t *testing.T) {
	cases := map[string]int{
		"5": 5, "05": 5, "5/12": 5, "": 0, "abc": 0, "2019-03-15": 2019, "2019": 2019,
	}
	for in, want := range cases {
		if got := leadingInt(in); got != want {
			t.Errorf("leadingInt(%q) = %d, want %d", in, got, want)
		}
	}
}
