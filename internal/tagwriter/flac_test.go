package tagwriter

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/tagreader"
)

// buildFLACFile writes a minimal but structurally real FLAC file: a
// zeroed (but correctly sized) STREAMINFO block, a Vorbis comment block
// seeded with the given comments, and a trailing chunk of fake "audio
// frame" bytes that a correct writer must carry through untouched.
func buildFLACFile(t *testing.T, comments map[string]string, audioData []byte) string {
	t.Helper()

	var file bytes.Buffer
	file.WriteString("fLaC")

	// STREAMINFO (type 0), not last — 34-byte payload, contents unused by
	// go-flac for this package's purposes (it round-trips the block
	// as-is unless GetStreamInfo/decoding is explicitly requested).
	file.WriteByte(0x00)
	file.Write([]byte{0, 0, 34})
	file.Write(make([]byte, 34))

	var vc bytes.Buffer
	writeU32LE(&vc, 0) // vendor length
	writeU32LE(&vc, uint32(len(comments)))
	for k, v := range comments {
		c := k + "=" + v
		writeU32LE(&vc, uint32(len(c)))
		vc.WriteString(c)
	}
	file.WriteByte(0x80 | 4) // last block, vorbis comment type
	n := vc.Len()
	file.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)})
	file.Write(vc.Bytes())

	// go-flac requires the frame stream to open with a real FLAC frame
	// sync code (0xFF, then top 6 bits of the next byte == 0x3E — see
	// go-flac's checkFLACStream/ErrorNoSyncCode) before it'll accept the
	// rest as opaque frame data to round-trip untouched.
	file.Write([]byte{0xFF, 0xF8})
	file.Write(audioData)

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

func TestWriteFLACVorbisCommentSetsNewFieldsAndPreservesAudio(t *testing.T) {
	audioData := []byte("fake FLAC audio frames that must survive untouched")
	path := buildFLACFile(t, map[string]string{"GENRE": "Electronic"}, audioData)

	tags := Tags{
		Title: "Alpha and Omega", Artist: "Boards of Canada", Album: "Geogaddi",
		TrackNumber: 3, DiscNumber: 1, Year: "2002",
		MusicBrainzArtistID:    "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		MusicBrainzRecordingID: "66666666-7777-8888-9999-000000000000",
	}
	if err := Write(path, tags); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if got.Title != tags.Title || got.Artist != tags.Artist || got.Album != tags.Album {
		t.Errorf("got Title=%q Artist=%q Album=%q, want %q/%q/%q", got.Title, got.Artist, got.Album, tags.Title, tags.Artist, tags.Album)
	}
	if got.MusicBrainzRecordingID != tags.MusicBrainzRecordingID {
		t.Errorf("MusicBrainzRecordingID = %q, want %q", got.MusicBrainzRecordingID, tags.MusicBrainzRecordingID)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, audioData) {
		t.Error("audio data was not preserved byte-for-byte after tag write")
	}
}

func TestWriteFLACVorbisCommentPreservesUnrelatedExistingFields(t *testing.T) {
	path := buildFLACFile(t, map[string]string{"GENRE": "Electronic", "COMMENT": "ripped by hand"}, []byte("audio"))

	if err := Write(path, Tags{Title: "New Title"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("GENRE=Electronic")) {
		t.Error("existing GENRE field was lost, want it preserved")
	}
	if !bytes.Contains(raw, []byte("COMMENT=ripped by hand")) {
		t.Error("existing COMMENT field was lost, want it preserved")
	}
}

func TestWriteFLACVorbisCommentReplacesRatherThanDuplicates(t *testing.T) {
	path := buildFLACFile(t, map[string]string{"TITLE": "Old Title"}, []byte("audio"))

	if err := Write(path, Tags{Title: "New Title"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New Title" {
		t.Errorf("Title = %q, want New Title (old value should be replaced, not duplicated)", got.Title)
	}
}
