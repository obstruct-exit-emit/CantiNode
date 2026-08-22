package tagreader

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestIsAudioFile(t *testing.T) {
	cases := map[string]bool{
		"song.mp3": true, "song.flac": true, "song.m4a": true,
		"song.ogg": true, "song.MP3": true, "song.opus": true, "song.wav": true,
		"cover.jpg": false, "readme.txt": false, "song.wma": false,
	}
	for name, want := range cases {
		if got := IsAudioFile(name); got != want {
			t.Errorf("IsAudioFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// --- FLAC (Vorbis comment) fixture ---

func buildFLACFile(t *testing.T, comments map[string]string) string {
	t.Helper()

	var block bytes.Buffer
	writeU32LE(&block, 0) // vendor length
	writeU32LE(&block, uint32(len(comments)))
	for k, v := range comments {
		c := k + "=" + v
		writeU32LE(&block, uint32(len(c)))
		block.WriteString(c)
	}

	var file bytes.Buffer
	file.WriteString("fLaC")
	file.WriteByte(0x80 | 4) // last block, vorbis comment block type
	blockBytes := block.Bytes()
	n := len(blockBytes)
	file.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)}) // 24-bit big-endian length
	file.Write(blockBytes)

	path := filepath.Join(t.TempDir(), "test.flac")
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeU32LE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func TestReadFLACVorbisComments(t *testing.T) {
	path := buildFLACFile(t, map[string]string{
		"TITLE":                      "Alpha and Omega",
		"ARTIST":                     "Boards of Canada",
		"ALBUM":                      "Geogaddi",
		"TRACKNUMBER":                "3",
		"DATE":                       "2002",
		"MUSICBRAINZ_ARTISTID":       "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		"MUSICBRAINZ_ALBUMID":        "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d",
		"MUSICBRAINZ_RELEASEGROUPID": "11111111-2222-3333-4444-555555555555",
		"MUSICBRAINZ_TRACKID":        "66666666-7777-8888-9999-000000000000",
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.Format != "flac" {
		t.Errorf("Format = %q, want flac", tags.Format)
	}
	if tags.Title != "Alpha and Omega" {
		t.Errorf("Title = %q", tags.Title)
	}
	if tags.Artist != "Boards of Canada" {
		t.Errorf("Artist = %q", tags.Artist)
	}
	if tags.Album != "Geogaddi" {
		t.Errorf("Album = %q", tags.Album)
	}
	if tags.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", tags.TrackNumber)
	}
	if tags.MusicBrainzArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("MusicBrainzArtistID = %q", tags.MusicBrainzArtistID)
	}
	if tags.MusicBrainzAlbumID != "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d" {
		t.Errorf("MusicBrainzAlbumID = %q", tags.MusicBrainzAlbumID)
	}
	if tags.MusicBrainzReleaseGroupID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("MusicBrainzReleaseGroupID = %q", tags.MusicBrainzReleaseGroupID)
	}
	if tags.MusicBrainzRecordingID != "66666666-7777-8888-9999-000000000000" {
		t.Errorf("MusicBrainzRecordingID = %q", tags.MusicBrainzRecordingID)
	}
}

// TestReadFLACNewFields covers the Vorbis-comment side of the same fields
// TestReadID3v2NewFieldAliases covers for ID3v2 — here every key is
// already the plain, direct name (no aliasing needed), confirmed against
// a real file written by internal/tagwriter.
func TestReadFLACNewFields(t *testing.T) {
	path := buildFLACFile(t, map[string]string{
		"TITLE":                     "Alpha and Omega",
		"GENRE":                     "Electronic; IDM",
		"RELEASETYPE":               "Album",
		"ARTISTSORT":                "Boards of Canada",
		"ALBUMARTISTSORT":           "Boards of Canada",
		"MUSICBRAINZ_ALBUMARTISTID": "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		"RELEASECOUNTRY":            "GB",
		"RELEASESTATUS":             "official",
		"MEDIA":                     "CD",
		"TRACKTOTAL":                "12",
		"DISCTOTAL":                 "1",
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.Genre != "Electronic; IDM" {
		t.Errorf("Genre = %q", tags.Genre)
	}
	if tags.ReleaseType != "Album" {
		t.Errorf("ReleaseType = %q", tags.ReleaseType)
	}
	if tags.ArtistSortName != "Boards of Canada" {
		t.Errorf("ArtistSortName = %q", tags.ArtistSortName)
	}
	if tags.AlbumArtistSortName != "Boards of Canada" {
		t.Errorf("AlbumArtistSortName = %q", tags.AlbumArtistSortName)
	}
	if tags.AlbumArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("AlbumArtistID = %q", tags.AlbumArtistID)
	}
	if tags.ReleaseCountry != "GB" {
		t.Errorf("ReleaseCountry = %q", tags.ReleaseCountry)
	}
	if tags.ReleaseStatus != "official" {
		t.Errorf("ReleaseStatus = %q", tags.ReleaseStatus)
	}
	if tags.Media != "CD" {
		t.Errorf("Media = %q", tags.Media)
	}
	if tags.TrackTotal != 12 {
		t.Errorf("TrackTotal = %d, want 12", tags.TrackTotal)
	}
	if tags.DiscTotal != 1 {
		t.Errorf("DiscTotal = %d, want 1", tags.DiscTotal)
	}
}

func TestReadFLACWithoutMusicBrainzIDs(t *testing.T) {
	path := buildFLACFile(t, map[string]string{
		"TITLE":  "Untagged Song",
		"ARTIST": "Unknown Artist",
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.MusicBrainzRecordingID != "" {
		t.Errorf("MusicBrainzRecordingID = %q, want empty", tags.MusicBrainzRecordingID)
	}
}

// --- ID3v2.3 (MP3) fixture ---

func id3v2Frame(name string, content []byte) []byte {
	var b bytes.Buffer
	b.WriteString(name)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(content)))
	b.Write(size[:])
	b.Write([]byte{0, 0}) // flags
	b.Write(content)
	return b.Bytes()
}

func tFrameContent(text string) []byte {
	return append([]byte{0x00}, []byte(text)...) // encoding=ISO-8859-1
}

func txxxFrameContent(description, text string) []byte {
	var b bytes.Buffer
	b.WriteByte(0x00) // encoding=ISO-8859-1
	b.WriteString(description)
	b.WriteByte(0x00)
	b.WriteString(text)
	return b.Bytes()
}

func ufidFrameContent(provider string, identifier string) []byte {
	var b bytes.Buffer
	b.WriteString(provider)
	b.WriteByte(0x00)
	b.WriteString(identifier)
	return b.Bytes()
}

func synchsafe(n int) [4]byte {
	return [4]byte{
		byte((n >> 21) & 0x7F),
		byte((n >> 14) & 0x7F),
		byte((n >> 7) & 0x7F),
		byte(n & 0x7F),
	}
}

func buildID3v23File(t *testing.T, frames [][]byte) string {
	t.Helper()

	var framesBytes bytes.Buffer
	for _, f := range frames {
		framesBytes.Write(f)
	}

	var file bytes.Buffer
	file.WriteString("ID3")
	file.Write([]byte{3, 0}) // version 2.3, revision 0
	file.WriteByte(0)        // flags
	size := synchsafe(framesBytes.Len())
	file.Write(size[:])
	file.Write(framesBytes.Bytes())

	path := filepath.Join(t.TempDir(), "test.mp3")
	if err := os.WriteFile(path, file.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadID3v2Tags(t *testing.T) {
	path := buildID3v23File(t, [][]byte{
		id3v2Frame("TIT2", tFrameContent("Alpha and Omega")),
		id3v2Frame("TPE1", tFrameContent("Boards of Canada")),
		id3v2Frame("TALB", tFrameContent("Geogaddi")),
		id3v2Frame("TRCK", tFrameContent("3")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Artist Id", "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Album Id", "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Release Group Id", "11111111-2222-3333-4444-555555555555")),
		id3v2Frame("UFID", ufidFrameContent(musicBrainzUFIDProvider, "66666666-7777-8888-9999-000000000000")),
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.Format != "mp3" {
		t.Errorf("Format = %q, want mp3", tags.Format)
	}
	if tags.Title != "Alpha and Omega" {
		t.Errorf("Title = %q", tags.Title)
	}
	if tags.Artist != "Boards of Canada" {
		t.Errorf("Artist = %q", tags.Artist)
	}
	if tags.Album != "Geogaddi" {
		t.Errorf("Album = %q", tags.Album)
	}
	if tags.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", tags.TrackNumber)
	}
	if tags.MusicBrainzArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("MusicBrainzArtistID = %q", tags.MusicBrainzArtistID)
	}
	if tags.MusicBrainzAlbumID != "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d" {
		t.Errorf("MusicBrainzAlbumID = %q", tags.MusicBrainzAlbumID)
	}
	if tags.MusicBrainzReleaseGroupID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("MusicBrainzReleaseGroupID = %q", tags.MusicBrainzReleaseGroupID)
	}
	// The recording ID comes from the UFID frame, not a TXXX frame.
	if tags.MusicBrainzRecordingID != "66666666-7777-8888-9999-000000000000" {
		t.Errorf("MusicBrainzRecordingID = %q", tags.MusicBrainzRecordingID)
	}
}

// TestReadID3v2NewFieldAliases covers the fields whose ID3v2 raw key
// doesn't match their Vorbis/MP4 name (see extractRawTextFields's own doc
// comment): a field with its own dedicated ID3v2 frame (TSOP/TSO2/TMED)
// decodes to a plain string keyed by that raw frame ID, and one that only
// exists as a custom field (release type/status/country, album artist
// id, track/disc totals) goes through TXXX under TagLib's own, older
// MusicBrainz-prefixed naming for some of these — confirmed live against
// a real file written by internal/tagwriter.
func TestReadID3v2NewFieldAliases(t *testing.T) {
	path := buildID3v23File(t, [][]byte{
		id3v2Frame("TIT2", tFrameContent("Alpha and Omega")),
		id3v2Frame("TCON", tFrameContent("Electronic; IDM")),
		id3v2Frame("TSOP", tFrameContent("Boards of Canada")),
		id3v2Frame("TSO2", tFrameContent("Boards of Canada")),
		id3v2Frame("TMED", tFrameContent("CD")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Album Type", "Album")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Album Artist Id", "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Album Release Country", "GB")),
		id3v2Frame("TXXX", txxxFrameContent("MusicBrainz Album Status", "official")),
		id3v2Frame("TXXX", txxxFrameContent("TRACKTOTAL", "12")),
		id3v2Frame("TXXX", txxxFrameContent("DISCTOTAL", "1")),
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.Genre != "Electronic; IDM" {
		t.Errorf("Genre = %q", tags.Genre)
	}
	if tags.ArtistSortName != "Boards of Canada" {
		t.Errorf("ArtistSortName = %q, want Boards of Canada (from raw TSOP)", tags.ArtistSortName)
	}
	if tags.AlbumArtistSortName != "Boards of Canada" {
		t.Errorf("AlbumArtistSortName = %q, want Boards of Canada (from raw TSO2)", tags.AlbumArtistSortName)
	}
	if tags.Media != "CD" {
		t.Errorf("Media = %q, want CD (from raw TMED)", tags.Media)
	}
	if tags.ReleaseType != "Album" {
		t.Errorf("ReleaseType = %q, want Album (from TXXX MusicBrainz Album Type)", tags.ReleaseType)
	}
	if tags.AlbumArtistID != "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d" {
		t.Errorf("AlbumArtistID = %q", tags.AlbumArtistID)
	}
	if tags.ReleaseCountry != "GB" {
		t.Errorf("ReleaseCountry = %q", tags.ReleaseCountry)
	}
	if tags.ReleaseStatus != "official" {
		t.Errorf("ReleaseStatus = %q", tags.ReleaseStatus)
	}
	if tags.TrackTotal != 12 {
		t.Errorf("TrackTotal = %d, want 12", tags.TrackTotal)
	}
	if tags.DiscTotal != 1 {
		t.Errorf("DiscTotal = %d, want 1", tags.DiscTotal)
	}
}

func TestReadID3v2WithoutMusicBrainzIDs(t *testing.T) {
	path := buildID3v23File(t, [][]byte{
		id3v2Frame("TIT2", tFrameContent("Untagged Song")),
	})

	tags, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.MusicBrainzArtistID != "" || tags.MusicBrainzRecordingID != "" {
		t.Errorf("expected no MusicBrainz IDs, got %+v", tags)
	}
}

// TestExtractMusicBrainzIDsTrimsMP4FreeformPadding is the regression test
// for a real bug found live: dhowden/tag's own MP4 atom parser
// (readCustomAtom in mp4.go) under-skips a freeform "data" sub-atom's
// 8-byte type+locale header by 4 bytes, so every custom MP4 tag —
// including exactly the "MusicBrainz Album/Artist/Release Group Id" atoms
// internal/tagwriter's new M4A support writes — comes back from Raw()
// with 4 leading NUL bytes still attached. Confirmed against a real file
// written by go.senan.xyz/taglib (which itself writes the standard,
// correct 8-byte header) before this fix landed. Vorbis comments hit the
// same code path in extractRawTextFields but are never affected (plain
// UTF-8 text has no legitimate reason to start with a NUL byte), so this
// only needs a synthetic map mimicking dhowden/tag's specific MP4
// mis-parse, not a full binary fixture.
func TestExtractMusicBrainzIDsTrimsMP4FreeformPadding(t *testing.T) {
	got := extractRawTextFields(map[string]interface{}{
		"MusicBrainz Album Id": "\x00\x00\x00\x00a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d",
	})
	want := "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"
	if got["musicbrainzalbumid"] != want {
		t.Errorf("musicbrainzalbumid = %q, want %q", got["musicbrainzalbumid"], want)
	}
}

// TestReadM4AFallsBackToExtensionForFormat is the regression test for a
// severe real bug found in review: dhowden/tag's metadataMP4.fileType is
// initialized to UnknownFileType ("") in ReadAtoms (mp4.go) and never
// assigned anywhere afterward, so FileType() — and therefore Read's own
// Format field — was "" for every M4A/M4B/M4P file, confirmed both
// directly against the pinned dependency source and empirically against
// this exact fixture before the fix. That empty string flowed straight
// into track_files.format in the database and from there into the album
// page's write-tags button gate (tagWritableFormats.has(f.format...)),
// silently hiding the button for exactly the file format this whole
// feature was built to support.
func TestReadM4AFallsBackToExtensionForFormat(t *testing.T) {
	tags, err := Read(filepath.Join("testdata", "sample.m4a"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if tags.Format != "m4a" {
		t.Errorf("Format = %q, want %q (dhowden/tag's own MP4 FileType() detection is always empty)", tags.Format, "m4a")
	}
}

func TestReadUnsupportedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-audio.mp3")
	if err := os.WriteFile(path, []byte("plain text, not a tag format"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("expected an error reading a file with no recognizable tags")
	}
}
