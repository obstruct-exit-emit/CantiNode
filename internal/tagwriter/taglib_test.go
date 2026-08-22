package tagwriter

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cantinode/cantinode/internal/tagreader"
	taglibpkg "go.senan.xyz/taglib"
)

// copyFixture copies testdata/name into a fresh temp dir and returns its
// new path — every test gets its own writable copy, since Write mutates
// the file in place and testdata/ itself must stay pristine across runs.
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

func fullTags() Tags {
	return Tags{
		Title: "Alpha and Omega", Artist: "Boards of Canada", Album: "Geogaddi",
		AlbumArtist: "Boards of Canada", TrackNumber: 3, DiscNumber: 1,
		TrackTotal: 12, DiscTotal: 1, Date: "2002-02-04",
		Genre: "Electronic; IDM", ReleaseType: "Album",
		ArtistSortName: "Boards of Canada", AlbumArtistSortName: "Boards of Canada",
		ReleaseCountry: "GB", ReleaseStatus: "official", Media: "CD",
		MusicBrainzArtistID:       "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		AlbumArtistID:             "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		MusicBrainzAlbumID:        "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d",
		MusicBrainzReleaseGroupID: "11111111-2222-3333-4444-555555555555",
		MusicBrainzRecordingID:    "66666666-7777-8888-9999-000000000000",
	}
}

// assertTagLibFields checks the new fields fullTags() sets that
// internal/tagreader has no getter for (Genre/ReleaseType/sort names/
// track-disc totals/AlbumArtistID) — read back via taglib's own reader
// instead of dhowden/tag. Still a real round-trip check, not the writer
// checking its own output: ReadTags and WriteTags are independent
// operations even within the same package, and TagLib itself is the
// mature, independent library the MP3 migration was about trusting in the
// first place.
func assertTagLibFields(t *testing.T, path string, tags Tags) {
	t.Helper()
	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("taglib ReadTags: %v", err)
	}
	check := func(key, want string) {
		t.Helper()
		if want == "" {
			return
		}
		vals := got[key]
		if len(vals) == 0 || vals[0] != want {
			t.Errorf("%s = %v, want %q", key, vals, want)
		}
	}
	check(taglibpkg.Genre, tags.Genre)
	check(taglibpkg.ReleaseType, tags.ReleaseType)
	check(taglibpkg.ArtistSort, tags.ArtistSortName)
	check(taglibpkg.AlbumArtistSort, tags.AlbumArtistSortName)
	check(taglibpkg.MusicBrainzAlbumArtistID, tags.AlbumArtistID)
	check(taglibpkg.ReleaseCountry, tags.ReleaseCountry)
	check(taglibpkg.ReleaseStatus, tags.ReleaseStatus)
	check(taglibpkg.Media, tags.Media)
	check(taglibpkg.Date, tags.Date)
	if tags.TrackTotal > 0 {
		check("TRACKTOTAL", strconv.Itoa(tags.TrackTotal))
	}
	if tags.DiscTotal > 0 {
		check("DISCTOTAL", strconv.Itoa(tags.DiscTotal))
	}
}

func assertTags(t *testing.T, path string, tags Tags) {
	t.Helper()
	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if got.Title != tags.Title {
		t.Errorf("Title = %q, want %q", got.Title, tags.Title)
	}
	if got.Artist != tags.Artist {
		t.Errorf("Artist = %q, want %q", got.Artist, tags.Artist)
	}
	if got.AlbumArtist != tags.AlbumArtist {
		t.Errorf("AlbumArtist = %q, want %q", got.AlbumArtist, tags.AlbumArtist)
	}
	if got.Album != tags.Album {
		t.Errorf("Album = %q, want %q", got.Album, tags.Album)
	}
	if got.TrackNumber != tags.TrackNumber {
		t.Errorf("TrackNumber = %d, want %d", got.TrackNumber, tags.TrackNumber)
	}
	if got.DiscNumber != tags.DiscNumber {
		t.Errorf("DiscNumber = %d, want %d", got.DiscNumber, tags.DiscNumber)
	}
	if got.MusicBrainzArtistID != tags.MusicBrainzArtistID {
		t.Errorf("MusicBrainzArtistID = %q, want %q", got.MusicBrainzArtistID, tags.MusicBrainzArtistID)
	}
	if got.MusicBrainzAlbumID != tags.MusicBrainzAlbumID {
		t.Errorf("MusicBrainzAlbumID = %q, want %q", got.MusicBrainzAlbumID, tags.MusicBrainzAlbumID)
	}
	if got.MusicBrainzReleaseGroupID != tags.MusicBrainzReleaseGroupID {
		t.Errorf("MusicBrainzReleaseGroupID = %q, want %q", got.MusicBrainzReleaseGroupID, tags.MusicBrainzReleaseGroupID)
	}
	// dhowden/tag surfaces the recording id under "MusicBrainz Track Id"
	// for MP4 (and "musicbrainz_trackid" for Vorbis comments) — the same
	// ecosystem-standard-but-confusingly-named field writeTagLib targets
	// via taglib.MusicBrainzTrackID; this is what actually proves that
	// mapping round-trips through the real read path, not just taglib's
	// own reader agreeing with itself.
	if got.MusicBrainzRecordingID != tags.MusicBrainzRecordingID {
		t.Errorf("MusicBrainzRecordingID = %q, want %q", got.MusicBrainzRecordingID, tags.MusicBrainzRecordingID)
	}
}

func TestWriteTagLibM4A(t *testing.T) {
	path := copyFixture(t, "sample.m4a")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
	assertTagLibFields(t, path, tags)
}

// TestWriteTagLibMP3 covers the format that used to be hand-rolled
// (id3v2.go, removed) — see tagwriter.go's package doc comment for why
// it moved here.
func TestWriteTagLibMP3(t *testing.T) {
	path := copyFixture(t, "sample.mp3")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
	assertTagLibFields(t, path, tags)
}

// TestWriteTagLibMP3NonASCIIText is the regression test for the bug that
// motivated moving MP3 off its old hand-rolled ID3v2 writer: a guest
// vocalist credit ("Avantasia, Hansi Kürsch, ... Jørn Lande, ...") came
// back as mojibake ("Hansi KÃ¼rsch", "JÃ¸rn Lande") after a round trip
// through that writer, which always labeled the ID3v2 encoding byte as
// ISO-8859-1 while writing raw UTF-8 bytes underneath. TagLib picks the
// correct encoding on its own.
func TestWriteTagLibMP3NonASCIIText(t *testing.T) {
	path := copyFixture(t, "sample.mp3")
	artist := "Avantasia, Hansi Kürsch, Ronnie Atkins, Jørn Lande, Mille Petrozza"
	if err := Write(path, Tags{Title: "Book of Shallows", Artist: artist, Album: "Moonglow"}, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if got.Artist != artist {
		t.Errorf("Artist = %q, want %q", got.Artist, artist)
	}
}

// TestWriteTagLibMP3PreservesUntrackedFields is TestWriteTagLibPreservesUntrackedFields
// for MP3 specifically — the other bug that motivated the same move: the
// old hand-rolled writer replaced the *entire* ID3v2 tag on every write,
// silently discarding any frame it didn't itself manage (genre, comments,
// embedded art, ...). sample_tagged.mp3 ships with GENRE/COMMENT/COMPOSER
// already set.
func TestWriteTagLibMP3PreservesUntrackedFields(t *testing.T) {
	path := copyFixture(t, "sample_tagged.mp3")
	before, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read fixture tags: %v", err)
	}
	if before[taglibpkg.Genre] == nil {
		t.Fatal("fixture sample_tagged.mp3 is expected to already have a GENRE tag — test assumption broken")
	}

	if err := Write(path, Tags{Title: "New Title", Artist: "New Artist"}, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	after, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after[taglibpkg.Genre]) == 0 || after[taglibpkg.Genre][0] != before[taglibpkg.Genre][0] {
		t.Errorf("GENRE = %v, want untouched (%v)", after[taglibpkg.Genre], before[taglibpkg.Genre])
	}
	if len(after[taglibpkg.Composer]) == 0 {
		t.Errorf("COMPOSER should survive untouched, got %v", after[taglibpkg.Composer])
	}
}

// TestWriteTagLibFLAC covers the format that used to be hand-rolled
// (flac.go, removed) — see tagwriter.go's package doc comment for why it
// moved here alongside MP3.
func TestWriteTagLibFLAC(t *testing.T) {
	path := copyFixture(t, "sample.flac")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
	assertTagLibFields(t, path, tags)
}

// TestWriteTagLibFLACPreservesUntrackedFields is
// TestWriteTagLibPreservesUntrackedFields for FLAC specifically —
// confirming the migration off the old hand-rolled writer (which already
// merged correctly) didn't regress that behavior. sample_tagged.flac
// ships with GENRE/COMPOSER already set.
func TestWriteTagLibFLACPreservesUntrackedFields(t *testing.T) {
	path := copyFixture(t, "sample_tagged.flac")
	before, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read fixture tags: %v", err)
	}
	if before[taglibpkg.Genre] == nil {
		t.Fatal("fixture sample_tagged.flac is expected to already have a GENRE tag — test assumption broken")
	}

	if err := Write(path, Tags{Title: "New Title", Artist: "New Artist"}, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	after, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after[taglibpkg.Genre]) == 0 || after[taglibpkg.Genre][0] != before[taglibpkg.Genre][0] {
		t.Errorf("GENRE = %v, want untouched (%v)", after[taglibpkg.Genre], before[taglibpkg.Genre])
	}
	if len(after[taglibpkg.Composer]) == 0 {
		t.Errorf("COMPOSER should survive untouched, got %v", after[taglibpkg.Composer])
	}
}

func TestWriteTagLibOGG(t *testing.T) {
	path := copyFixture(t, "sample.ogg")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
}

func TestWriteTagLibDSF(t *testing.T) {
	path := copyFixture(t, "sample.dsf")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
}

func TestWriteTagLibWAV(t *testing.T) {
	path := copyFixture(t, "sample.wav")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
}

// TestWriteTagLibOpus round-trips through internal/tagreader's real Read()
// (not just taglib reading its own write) the same way the other
// writeTagLib tests do — proving both halves of Opus support agree with
// each other, not just that each independently believes it works.
func TestWriteTagLibOpus(t *testing.T) {
	path := copyFixture(t, "sample.opus")
	tags := fullTags()
	if err := Write(path, tags, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertTags(t, path, tags)
}

// TestWriteTagLibPreservesUntrackedFields is the regression test for the
// real gap setField exists to close: WriteTags only touches keys present
// in the map, so a field CantiNode's own Tags struct doesn't know about
// (GENRE, COMMENT, ...) must survive a write completely untouched —
// verified against sample.dsf, which (unlike the other two fixtures)
// already ships with GENRE/COMMENT/COMPOSER set.
func TestWriteTagLibPreservesUntrackedFields(t *testing.T) {
	path := copyFixture(t, "sample.dsf")
	before, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read fixture tags: %v", err)
	}
	if before[taglibpkg.Genre] == nil {
		t.Fatal("fixture sample.dsf is expected to already have a GENRE tag — test assumption broken")
	}

	if err := Write(path, Tags{Title: "New Title", Artist: "New Artist"}, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	after, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after[taglibpkg.Genre]) == 0 || after[taglibpkg.Genre][0] != before[taglibpkg.Genre][0] {
		t.Errorf("GENRE = %v, want untouched (%v)", after[taglibpkg.Genre], before[taglibpkg.Genre])
	}
	if len(after[taglibpkg.Composer]) == 0 {
		t.Errorf("COMPOSER should survive untouched, got %v", after[taglibpkg.Composer])
	}
}

// TestWriteTagLibClearsEmptyField confirms setField's explicit-empty-slice
// behavior actually clears a stale value instead of leaving it in place —
// the gap that would exist if empty Tags fields were simply omitted from
// the map instead (WriteTags only touches keys it's given; omitting a key
// entirely leaves whatever was already there, which is wrong once a
// re-match genuinely has no album artist credit for a track that used to
// have one).
func TestWriteTagLibClearsEmptyField(t *testing.T) {
	path := copyFixture(t, "sample.m4a")
	if err := Write(path, Tags{Title: "T", AlbumArtist: "Stale Album Artist"}, false); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	seeded, err := tagreader.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if seeded.AlbumArtist != "Stale Album Artist" {
		t.Fatalf("seed AlbumArtist = %q, want it to have taken", seeded.AlbumArtist)
	}

	if err := Write(path, Tags{Title: "T", AlbumArtist: ""}, false); err != nil {
		t.Fatalf("clearing write: %v", err)
	}
	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlbumArtist != "" {
		t.Errorf("AlbumArtist = %q after clearing write, want empty", got.AlbumArtist)
	}
	if got.Title != "T" {
		t.Errorf("Title = %q, want it to survive the clearing write", got.Title)
	}
}

// TestWriteClearTrueWipesUntrackedFieldsFromMergeTruePreserves is the
// regression/acceptance test for the explicit "wipe first" mode: the same
// GENRE/COMPOSER a merge write (clear=false) leaves untouched must be
// gone after a clear=true write of the exact same Tags — proving clear
// actually reaches taglib.WriteOption, not just that the field-level
// setFieldIfPresent logic happens to look the same either way.
func TestWriteClearTrueWipesUntrackedFieldsFromMergeTruePreserves(t *testing.T) {
	path := copyFixture(t, "sample_tagged.mp3")
	before, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read fixture tags: %v", err)
	}
	if before[taglibpkg.Genre] == nil {
		t.Fatal("fixture sample_tagged.mp3 is expected to already have a GENRE tag — test assumption broken")
	}

	if err := Write(path, Tags{Title: "New Title"}, true); err != nil {
		t.Fatalf("Write (clear=true): %v", err)
	}

	after, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after[taglibpkg.Genre]) != 0 {
		t.Errorf("GENRE = %v after a clear=true write, want wiped", after[taglibpkg.Genre])
	}
	if len(after[taglibpkg.Composer]) != 0 {
		t.Errorf("COMPOSER = %v after a clear=true write, want wiped", after[taglibpkg.Composer])
	}
	if got := after[taglibpkg.Title]; len(got) == 0 || got[0] != "New Title" {
		t.Errorf("TITLE = %v, want [New Title] (clear must not remove fields the write itself sets)", got)
	}
}

// TestWriteEmbedsCoverImage confirms Tags.CoverImage actually reaches the
// file as a real embedded picture, readable back through taglib's own
// image API — the mechanism internal/musicscanner's WriteTags relies on
// to embed an album's cached cover art.
func TestWriteEmbedsCoverImage(t *testing.T) {
	path := copyFixture(t, "sample.mp3")
	fakeJPEG := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xD9} // minimal JPEG SOI/APP0/EOI markers, no real image data
	if err := Write(path, Tags{Title: "Cover Test", CoverImage: fakeJPEG}, false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := taglibpkg.ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if len(got) != len(fakeJPEG) {
		t.Errorf("embedded image = %d bytes, want %d", len(got), len(fakeJPEG))
	}
}
