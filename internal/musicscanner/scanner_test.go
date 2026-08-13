package musicscanner

import (
	"path/filepath"
	"testing"
)

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
