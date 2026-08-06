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
	}
	for _, tc := range cases {
		if got := sanitizeReleaseTitle(tc.in); got != tc.want {
			t.Errorf("sanitizeReleaseTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
