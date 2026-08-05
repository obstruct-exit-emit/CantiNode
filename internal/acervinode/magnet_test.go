package acervinode

import "testing"

func TestMagnetInfoHash(t *testing.T) {
	cases := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{"magnet:?xt=urn:btih:ABCDEF1234567890abcdef1234567890ABCDEF12", "abcdef1234567890abcdef1234567890abcdef12", false},
		{"magnet:?dn=Something&xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&tr=udp://tracker", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"https://example.com/not-a-magnet", "", true},
		{"magnet:no-query-string", "", true},
		{"magnet:?xt=urn:sha1:somethingelse", "", true},
	}
	for _, tc := range cases {
		got, err := magnetInfoHash(tc.uri)
		if tc.wantErr {
			if err == nil {
				t.Errorf("magnetInfoHash(%q) = %q, want an error", tc.uri, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("magnetInfoHash(%q) unexpected error: %v", tc.uri, err)
			continue
		}
		if got != tc.want {
			t.Errorf("magnetInfoHash(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}
