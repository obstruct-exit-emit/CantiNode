package comiccover

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// jpeg/png return minimal byte slices that begin with the real magic numbers
// imageSig checks for.
func jpeg(tag string) []byte { return append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte(tag)...) }
func png(tag string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(tag)...)
}

func writeCBZ(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipPicksFirstImageByName(t *testing.T) {
	cbz := filepath.Join(t.TempDir(), "Vol 1.cbz")
	writeCBZ(t, cbz, map[string][]byte{
		"002.png":     png("second"),
		"001.jpg":     jpeg("cover"),
		"credits.txt": []byte("not an image"),
	})

	data, ct, err := Extract(cbz)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Equal(data, jpeg("cover")) {
		t.Errorf("data = %q, want the 001.jpg cover bytes", data)
	}
	if ct != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", ct)
	}
}

// A page with an image extension but non-image bytes (a placeholder) is
// skipped in favor of the next real image.
func TestExtractSkipsNonImagePage(t *testing.T) {
	cbz := filepath.Join(t.TempDir(), "Vol 2.cbz")
	writeCBZ(t, cbz, map[string][]byte{
		"001.jpg": []byte("this is just text, not a jpeg"),
		"002.png": png("real"),
	})

	data, ct, err := Extract(cbz)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !bytes.Equal(data, png("real")) || ct != "image/png" {
		t.Errorf("got %q (%s), want the real 002.png", data, ct)
	}
}

func TestExtractNoImage(t *testing.T) {
	cbz := filepath.Join(t.TempDir(), "empty.cbz")
	writeCBZ(t, cbz, map[string][]byte{"readme.txt": []byte("no pages here")})
	if _, _, err := Extract(cbz); err == nil {
		t.Fatal("expected an error for an archive with no image")
	}
}

func TestExtractUnsupported(t *testing.T) {
	if _, _, err := Extract("/tmp/whatever.pdf"); err == nil {
		t.Fatal("expected an error for an unsupported archive type")
	}
}
