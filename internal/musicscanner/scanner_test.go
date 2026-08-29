package musicscanner

import (
	"path/filepath"
	"testing"
	"time"
)

// TestScanAllSerializesConcurrentCalls is the regression test for a real
// gap: ScanAll had no guard against running concurrently with itself.
// Nothing stopped the periodic importer sweep's own post-import ScanAll
// call (internal/importer.importGrab) from overlapping with a manual
// "Import now"/"Scan library" trigger's ScanAll call on the very same
// Scanner — two full-library scans racing on the same DB, matching (and
// writing) the same files at once. Found live as "downloading albums
// sometimes don't show up in Activity": a completed grab's post-copy scan
// interfered with by a second concurrent scan can come back having matched
// nothing, so importGrab reverts the album back to "wanted" even though
// its files really were copied in — intermittent, since it only bites
// when two scans' timing happens to overlap.
//
// Held externally here rather than actually racing two real scans against
// each other: real scans finish fast enough on a tiny test fixture that a
// timing-based race would be flaky. Locking scanMu directly and asserting
// ScanAll blocks until it's released proves the same thing deterministically.
func TestScanAllSerializesConcurrentCalls(t *testing.T) {
	s, _ := newTestScanner(t, nil, nil)

	s.scanMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.ScanAll(t.Context()); err != nil {
			t.Errorf("ScanAll: %v", err)
		}
	}()

	select {
	case <-done:
		t.Fatal("ScanAll returned while scanMu was still held externally — concurrent scans aren't serialized")
	case <-time.After(200 * time.Millisecond):
		// Still blocked, as expected.
	}

	s.scanMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ScanAll did not proceed after scanMu was released")
	}
}

// TestCommonAncestorDir exercises the helper ScanAlbumFolder uses to find
// one walk root covering every existing track file's directory — the
// single-directory case must stay a no-op (an ordinary single-disc album
// behaves exactly as before this fix), and a multi-disc layout must reduce
// to the shared parent, not a meaningless partial-string overlap.
func TestCommonAncestorDir(t *testing.T) {
	cases := []struct {
		name string
		dirs []string
		want string
	}{
		{
			name: "single directory",
			dirs: []string{filepath.FromSlash("/music/Pink Floyd/The Wall")},
			want: filepath.FromSlash("/music/Pink Floyd/The Wall"),
		},
		{
			name: "same directory repeated",
			dirs: []string{
				filepath.FromSlash("/music/Pink Floyd/The Wall"),
				filepath.FromSlash("/music/Pink Floyd/The Wall"),
			},
			want: filepath.FromSlash("/music/Pink Floyd/The Wall"),
		},
		{
			name: "two disc subfolders share the album folder",
			dirs: []string{
				filepath.FromSlash("/music/Avantasia/Moonglow (2CD)/CD1"),
				filepath.FromSlash("/music/Avantasia/Moonglow (2CD)/CD2"),
			},
			want: filepath.FromSlash("/music/Avantasia/Moonglow (2CD)"),
		},
		{
			name: "three disc subfolders",
			dirs: []string{
				filepath.FromSlash("/music/Box Set/CD1"),
				filepath.FromSlash("/music/Box Set/CD2"),
				filepath.FromSlash("/music/Box Set/CD3"),
			},
			want: filepath.FromSlash("/music/Box Set"),
		},
		{
			name: "album titles sharing a string prefix must not fool component-wise comparison",
			dirs: []string{
				filepath.FromSlash("/music/Album"),
				filepath.FromSlash("/music/Album2"),
			},
			want: filepath.FromSlash("/music"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commonAncestorDir(c.dirs); got != c.want {
				t.Errorf("commonAncestorDir(%v) = %q, want %q", c.dirs, got, c.want)
			}
		})
	}
}
