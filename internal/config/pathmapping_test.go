package config

import (
	"testing"
	"time"
)

func TestTranslatePath(t *testing.T) {
	mappings := []PathMapping{
		{RemotePrefix: "/storage_1", LocalPrefix: "/mnt/media"},
		{RemotePrefix: "/storage_1/books", LocalPrefix: "/srv/books"},
		{RemotePrefix: `C:\downloads`, LocalPrefix: "/mnt/dl"},
	}

	cases := []struct{ in, want string }{
		// Longest prefix wins.
		{"/storage_1/books/x.epub", "/srv/books/x.epub"},
		{"/storage_1/audio/y.m4b", "/mnt/media/audio/y.m4b"},
		// Exact prefix (no remainder).
		{"/storage_1", "/mnt/media"},
		// Boundary-aware: /storage_12 is not /storage_1.
		{"/storage_12/z.epub", "/storage_12/z.epub"},
		// Windows client path onto a Unix mount, separators converted.
		{`C:\downloads\Book Title\file.epub`, "/mnt/dl/Book Title/file.epub"},
		// Case-insensitive prefix match (Windows-style clients).
		{`c:\DOWNLOADS\a.cbz`, "/mnt/dl/a.cbz"},
		// Unmapped paths pass through.
		{"/other/place/file.pdf", "/other/place/file.pdf"},
		{"", ""},
	}
	for _, c := range cases {
		if got := TranslatePath(mappings, c.in); got != c.want {
			t.Errorf("TranslatePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	if got := TranslatePath(nil, "/storage_1/x"); got != "/storage_1/x" {
		t.Errorf("no mappings: got %q", got)
	}
}

func TestTimingDefaults(t *testing.T) {
	var ts TimingSettings // all zero = all defaults
	if got := ts.SearchInterval(); got != 6*time.Hour {
		t.Errorf("search default = %v, want 6h", got)
	}
	if got := ts.RefreshInterval(); got != 720*time.Hour {
		t.Errorf("refresh default = %v, want 720h (30 days)", got)
	}
	if got := ts.HealthInterval(); got != 15*time.Minute {
		t.Errorf("health default = %v, want 15m", got)
	}
	if got := ts.ImportInterval(); got != time.Minute {
		t.Errorf("import default = %v, want 1m", got)
	}
}
