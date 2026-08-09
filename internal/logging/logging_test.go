package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	// Tiny max size so a few writes force rotations; keep 2 old files.
	r, err := NewRotatingFile(path, 64, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	line := strings.Repeat("x", 30) + "\n" // ~31 bytes → rotate every 3rd write
	for i := 0; i < 10; i++ {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Live file plus .1 and .2 exist; .3 must not (keep=2).
	for _, p := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", p)
		}
		if info.Size() > 64+int64(len(line)) {
			t.Errorf("%s over max size: %d", p, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Error("keep=2 should never leave a .3 file")
	}
}
