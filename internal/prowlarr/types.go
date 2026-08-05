package prowlarr

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Protocol is a Release's download protocol. Prowlarr's own
// DownloadProtocol C# enum (Unknown=0, Usenet=1, Torrent=2) has been
// observed serialized both ways across *arr-family APIs depending on
// version/config (a bare integer, or its string name) — UnmarshalJSON
// below accepts either rather than assuming one, since this couldn't be
// verified against a live instance (see ROADMAP.md).
type Protocol string

const (
	ProtocolTorrent Protocol = "torrent"
	ProtocolUsenet  Protocol = "usenet"
	ProtocolUnknown Protocol = "unknown"
)

func (p *Protocol) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		switch strings.ToLower(s) {
		case "torrent":
			*p = ProtocolTorrent
		case "usenet":
			*p = ProtocolUsenet
		default:
			*p = ProtocolUnknown
		}
		return nil
	}

	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		switch n {
		case 2:
			*p = ProtocolTorrent
		case 1:
			*p = ProtocolUsenet
		default:
			*p = ProtocolUnknown
		}
		return nil
	}

	return fmt.Errorf("prowlarr: cannot unmarshal protocol from %s", b)
}

// Release is one search result — the fields CantiNode actually uses out
// of Prowlarr's own (much larger) ReleaseResource.
type Release struct {
	GUID        string    `json:"guid"`
	Title       string    `json:"title"`
	Size        int64     `json:"size"`
	IndexerID   int       `json:"indexerId"`
	Indexer     string    `json:"indexer"`
	PublishDate time.Time `json:"publishDate"`
	// DownloadURL/MagnetURL are already Prowlarr-proxied links (routed
	// back through Prowlarr itself, not the indexer directly) — see
	// internal/prowlarr's own doc comment and FetchContent, which
	// resolves whichever of these is set into either a real magnet URI
	// or the actual .torrent/.nzb bytes.
	DownloadURL string   `json:"downloadUrl"`
	MagnetURL   string   `json:"magnetUrl"`
	InfoURL     string   `json:"infoUrl"`
	Protocol    Protocol `json:"protocol"`
	Seeders     *int     `json:"seeders"`
	Leechers    *int     `json:"leechers"`
}

// FileName is the suggested filename for this release's content — the
// same rule Prowlarr's own ReleaseResource.FileName uses, in case a
// caller needs one (a magnet URI has no filename of its own).
func (r Release) FileName() string {
	switch r.Protocol {
	case ProtocolTorrent:
		return r.Title + ".torrent"
	case ProtocolUsenet:
		return r.Title + ".nzb"
	default:
		return r.Title
	}
}
