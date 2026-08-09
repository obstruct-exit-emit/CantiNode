package tagwriter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSupported(t *testing.T) {
	cases := map[string]bool{
		"song.mp3": true, "song.MP3": true, "song.flac": true,
		"song.m4a": false, "song.ogg": false, "song.wav": false,
	}
	for name, want := range cases {
		if got := IsSupported(name); got != want {
			t.Errorf("IsSupported(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestWriteUnsupportedFormatReturnsErrUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "song.m4a")
	if err := os.WriteFile(path, []byte("not a real m4a"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Write(path, Tags{Title: "X"})
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("err = %v, want ErrUnsupportedFormat", err)
	}
}
