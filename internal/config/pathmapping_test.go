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
	var ts TimingSettings // zero = default
	if got := ts.HealthInterval(); got != 15*time.Minute {
		t.Errorf("health default = %v, want 15m", got)
	}
}

func TestWantedSearchNextRunDailyMode(t *testing.T) {
	// Zero value defaults to "daily" mode at 03:00.
	var ts TimingSettings
	if ts.WantedSearchMode != "" {
		t.Fatalf("precondition: want zero-value mode, got %q", ts.WantedSearchMode)
	}

	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before today's fire time",
			now:  time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC),
			want: time.Date(2026, 8, 12, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "exactly at fire time — already fired, next is tomorrow",
			now:  time.Date(2026, 8, 12, 3, 0, 0, 0, time.UTC),
			want: time.Date(2026, 8, 13, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "after today's fire time",
			now:  time.Date(2026, 8, 12, 14, 30, 0, 0, time.UTC),
			want: time.Date(2026, 8, 13, 3, 0, 0, 0, time.UTC),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ts.WantedSearchNextRun(c.now); !got.Equal(c.want) {
				t.Errorf("WantedSearchNextRun(%v) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

func TestWantedSearchNextRunDailyModeCustomTime(t *testing.T) {
	ts := TimingSettings{WantedSearchTimeOfDay: "23:15"}
	now := time.Date(2026, 8, 12, 22, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 12, 23, 15, 0, 0, time.UTC)
	if got := ts.WantedSearchNextRun(now); !got.Equal(want) {
		t.Errorf("WantedSearchNextRun(%v) = %v, want %v", now, got, want)
	}
}

func TestWantedSearchNextRunIntervalMode(t *testing.T) {
	ts := TimingSettings{WantedSearchMode: WantedSearchModeInterval, WantedSearchIntervalMinutes: 90}
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	want := now.Add(90 * time.Minute)
	if got := ts.WantedSearchNextRun(now); !got.Equal(want) {
		t.Errorf("WantedSearchNextRun(%v) = %v, want %v", now, got, want)
	}
}

func TestWantedSearchNextRunIntervalModeDefault(t *testing.T) {
	ts := TimingSettings{WantedSearchMode: WantedSearchModeInterval} // zero minutes = default (24h)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	want := now.Add(24 * time.Hour)
	if got := ts.WantedSearchNextRun(now); !got.Equal(want) {
		t.Errorf("WantedSearchNextRun(%v) = %v, want %v", now, got, want)
	}
}

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in         string
		wantHour   int
		wantMinute int
		wantOK     bool
	}{
		{"03:00", 3, 0, true},
		{"23:59", 23, 59, true},
		{"0:5", 0, 5, true},
		{" 09:30 ", 9, 30, true},
		{"24:00", 0, 0, false},
		{"12:60", 0, 0, false},
		{"noon", 0, 0, false},
		{"", 0, 0, false},
		{"12", 0, 0, false},
		{"-1:00", 0, 0, false},
	}
	for _, c := range cases {
		h, m, ok := parseHHMM(c.in)
		if ok != c.wantOK || (ok && (h != c.wantHour || m != c.wantMinute)) {
			t.Errorf("parseHHMM(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.in, h, m, ok, c.wantHour, c.wantMinute, c.wantOK)
		}
	}
}
