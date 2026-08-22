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
		"song.m4a": true, "song.m4b": true, "song.m4p": true,
		"song.ogg": true, "song.oga": true, "song.opus": true,
		"song.dsf": true, "song.wav": true,
		"song.wma": false, "song.aiff": false,
	}
	for name, want := range cases {
		if got := IsSupported(name); got != want {
			t.Errorf("IsSupported(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestWriteUnsupportedFormatReturnsErrUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "song.wma")
	if err := os.WriteFile(path, []byte("not a real wma"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Write(path, Tags{Title: "X"}, false, AllEnabled)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("err = %v, want ErrUnsupportedFormat", err)
	}
}
