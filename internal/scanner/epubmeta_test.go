package scanner

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeEpub builds a minimal epub (a zip with container.xml + an OPF) at a temp
// path and returns it. entries maps archive names to contents.
func writeEpub(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
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
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const containerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

func TestEpubIdentifiers(t *testing.T) {
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf" version="2.0">
  <metadata>
    <dc:identifier opf:scheme="ISBN">urn:isbn:9780553380163</dc:identifier>
    <dc:identifier opf:scheme="AMAZON">B0072XL8BC</dc:identifier>
    <dc:title>A Game of Thrones</dc:title>
  </metadata>
</package>`
	path := writeEpub(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"content.opf":            opf,
	})
	isbn, asin := EpubIdentifiers(path)
	if isbn != "9780553380163" {
		t.Errorf("isbn = %q, want 9780553380163", isbn)
	}
	if asin != "B0072XL8BC" {
		t.Errorf("asin = %q, want B0072XL8BC", asin)
	}
}

func TestEpubIdentifiersNoneAndBadFile(t *testing.T) {
	// An OPF with no usable identifier yields nothing.
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="2.0">
  <metadata><dc:identifier>some-internal-uuid</dc:identifier><dc:title>X</dc:title></metadata>
</package>`
	path := writeEpub(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"content.opf":            opf,
	})
	if isbn, asin := EpubIdentifiers(path); isbn != "" || asin != "" {
		t.Errorf("no-identifier epub = (%q, %q), want empty", isbn, asin)
	}

	// A non-zip file (wrong extension on random bytes) must not panic.
	bad := filepath.Join(t.TempDir(), "notreally.epub")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isbn, asin := EpubIdentifiers(bad); isbn != "" || asin != "" {
		t.Errorf("bad file = (%q, %q), want empty", isbn, asin)
	}
}
