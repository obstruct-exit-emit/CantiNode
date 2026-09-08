package musicbrainz

import "testing"

func TestSanitizeReleaseTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Layla and Other Assorted Love Songs SHM-CD", "Layla and Other Assorted Love Songs"},
		{"Layla and Other Assorted Love Songs (Polydor.2011) 24-96 hdtracks", "Layla and Other Assorted Love Songs (Polydor.2011)"},
		{"Kind of Blue (24-Bit Remaster)", "Kind of Blue (Remaster)"},
		{"Rumours [FLAC]", "Rumours"},
		{"Rumours [FLAC 320kbps]", "Rumours"},
		{"Nevermind (Deluxe Edition)", "Nevermind (Deluxe Edition)"}, // legitimate release words left alone
		{"OK Computer", "OK Computer"},                               // nothing to strip
		{"", ""},
		// Regression: a whitespace-padded subtitle separator breaks
		// MusicBrainz's own phrase-query search entirely (confirmed live —
		// zero results for "The Mystery Of Time - A Rock Epic" against a
		// real MusicBrainz title of "The Mystery of Time: A Rock Epic",
		// even though every word matches) — collapsed to a plain space so
		// the query becomes a simple word sequence with no stray token.
		{"The Mystery Of Time - A Rock Epic", "The Mystery Of Time A Rock Epic"},
		{"Album – Subtitle", "Album Subtitle"}, // en dash
		{"Album — Subtitle", "Album Subtitle"}, // em dash
		{"Now and Then - Yesterday - Tomorrow", "Now and Then Yesterday Tomorrow"},
		// A colon/dash glued directly to a word (no surrounding space) is
		// a real word or a title's own real punctuation, not a stray
		// separator token — left alone.
		{"The Mystery of Time: A Rock Epic", "The Mystery of Time: A Rock Epic"},
		{"Sci-Fi Album", "Sci-Fi Album"},
	}
	for _, tc := range cases {
		if got := sanitizeReleaseTitle(tc.in); got != tc.want {
			t.Errorf("sanitizeReleaseTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
