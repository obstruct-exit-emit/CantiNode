package tagwriter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/tagreader"
)

func TestWriteID3v2ToFileWithNoExistingTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.mp3")
	audioData := []byte("fake mpeg audio frames, not real, just needs to survive round-trip")
	if err := os.WriteFile(path, audioData, 0o644); err != nil {
		t.Fatal(err)
	}

	tags := Tags{
		Title: "Alpha and Omega", Artist: "Boards of Canada", Album: "Geogaddi",
		AlbumArtist: "Boards of Canada", TrackNumber: 3, DiscNumber: 1, Year: "2002",
		MusicBrainzArtistID:       "8b19a412-58a1-40e1-8c1d-9e3ea50e0f9d",
		MusicBrainzAlbumID:        "a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d",
		MusicBrainzReleaseGroupID: "11111111-2222-3333-4444-555555555555",
		MusicBrainzRecordingID:    "66666666-7777-8888-9999-000000000000",
	}
	if err := Write(path, tags); err != nil {
		t.Fatalf("Write: %v", err)
	}

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
	if got.Album != tags.Album {
		t.Errorf("Album = %q, want %q", got.Album, tags.Album)
	}
	if got.TrackNumber != tags.TrackNumber {
		t.Errorf("TrackNumber = %d, want %d", got.TrackNumber, tags.TrackNumber)
	}
	if got.MusicBrainzArtistID != tags.MusicBrainzArtistID {
		t.Errorf("MusicBrainzArtistID = %q, want %q", got.MusicBrainzArtistID, tags.MusicBrainzArtistID)
	}
	if got.MusicBrainzRecordingID != tags.MusicBrainzRecordingID {
		t.Errorf("MusicBrainzRecordingID = %q, want %q", got.MusicBrainzRecordingID, tags.MusicBrainzRecordingID)
	}

	// The audio bytes after the tag must survive untouched.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	size, err := existingID3v2Size(mustOpen(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[size:]) != string(audioData) {
		t.Errorf("audio data after tag = %q, want %q", raw[size:], audioData)
	}
}

func TestWriteID3v2ReplacesExistingTagWithoutCorruptingAudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.mp3")
	audioData := []byte("second fake audio payload, distinct from the first test")

	// Build a file that already has an ID3v2 tag (old title) followed by
	// the audio bytes, then overwrite with new tags — the old tag must be
	// fully discarded, not appended to or left partially in place.
	oldTag := buildID3v2Tag(Tags{Title: "Old Title", Artist: "Old Artist"})
	if err := os.WriteFile(path, append(oldTag, audioData...), 0o644); err != nil {
		t.Fatal(err)
	}

	newTags := Tags{Title: "New Title", Artist: "New Artist", Album: "New Album", TrackNumber: 1}
	if err := Write(path, newTags); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if got.Title != "New Title" || got.Artist != "New Artist" {
		t.Errorf("got Title=%q Artist=%q, want New Title/New Artist", got.Title, got.Artist)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	size, err := existingID3v2Size(mustOpen(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[size:]) != string(audioData) {
		t.Errorf("audio data after replaced tag = %q, want %q (old tag or audio data corrupted)", raw[size:], audioData)
	}
}

func TestWriteID3v2EmptyFieldsOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Tags{Title: "Only Title"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := tagreader.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Only Title" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Artist != "" || got.MusicBrainzArtistID != "" {
		t.Errorf("expected empty Artist/MusicBrainzArtistID, got Artist=%q MBID=%q", got.Artist, got.MusicBrainzArtistID)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
