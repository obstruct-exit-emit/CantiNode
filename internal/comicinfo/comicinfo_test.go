package comicinfo

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeCbz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		entry.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func readEntry(t *testing.T, path, name string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, entry := range r.File {
		if entry.Name == name {
			rc, _ := entry.Open()
			defer rc.Close()
			data, _ := io.ReadAll(rc)
			return string(data)
		}
	}
	return ""
}

func TestInject(t *testing.T) {
	cbz := filepath.Join(t.TempDir(), "Berserk v05.cbz")
	makeCbz(t, cbz, map[string]string{
		"page01.jpg":    "img1",
		"ComicInfo.xml": "<ComicInfo><Series>stale</Series></ComicInfo>",
	})

	err := Inject(cbz, Info{Series: "Berserk", Number: "5", Writer: "Kentarou Miura", Year: 1990})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	xml := readEntry(t, cbz, "ComicInfo.xml")
	for _, want := range []string{"<Series>Berserk</Series>", "<Number>5</Number>", "<Writer>Kentarou Miura</Writer>", "<Year>1990</Year>"} {
		if !strings.Contains(xml, want) {
			t.Errorf("ComicInfo.xml missing %s:\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "stale") {
		t.Error("old ComicInfo.xml not replaced")
	}
	if readEntry(t, cbz, "page01.jpg") != "img1" {
		t.Error("page content lost during rewrite")
	}

	// Non-cbz is a quiet no-op.
	if err := Inject(filepath.Join(t.TempDir(), "x.cbr"), Info{}); err != nil {
		t.Errorf("cbr inject should no-op: %v", err)
	}
}
