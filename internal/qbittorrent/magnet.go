package qbittorrent

import (
	"fmt"
	"net/url"
	"strings"
)

// magnetInfoHash extracts the BitTorrent infohash from a magnet URI's
// xt=urn:btih:<hash> parameter, lowercased — matching a real qBittorrent's
// own case-insensitive-by-lowercasing hash handling, so GetStatus's later
// lookup is consistent regardless of how the magnet URI itself cased it.
//
// Parsed by splitting on the first "?" and reading the rest as a plain
// query string, rather than url.Parse on the whole URI — "magnet:"
// followed directly by "?" (no "//" authority) is a valid but unusual
// shape that's simpler to handle this way than to rely on how url.Parse
// happens to bucket it between Opaque/Path/RawQuery.
func magnetInfoHash(magnetURI string) (string, error) {
	if !strings.HasPrefix(magnetURI, "magnet:") {
		return "", fmt.Errorf("qbittorrent: not a magnet URI: %q", magnetURI)
	}
	_, query, ok := strings.Cut(magnetURI, "?")
	if !ok {
		return "", fmt.Errorf("qbittorrent: magnet URI has no query parameters: %q", magnetURI)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("qbittorrent: parse magnet query: %w", err)
	}
	for _, xt := range values["xt"] {
		if rest, ok := strings.CutPrefix(xt, "urn:btih:"); ok {
			return strings.ToLower(rest), nil
		}
	}
	return "", fmt.Errorf("qbittorrent: magnet URI has no urn:btih xt parameter: %q", magnetURI)
}
