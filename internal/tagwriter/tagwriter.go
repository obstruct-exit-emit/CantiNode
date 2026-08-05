// Package tagwriter writes corrected metadata back into an audio file's
// own tags, once internal/scanner has matched it to a MusicBrainz
// recording — the "fix the actual file, not just CantiNode's database"
// half of organizing a library. Unlike internal/tagreader (which reads
// every format dhowden/tag supports), writing is deliberately narrower:
// MP3 (ID3v2) and FLAC (Vorbis comments) only for v1, both implemented
// against well-understood, low-risk formats. MP4/M4A tag writing needs
// correctly rewriting nested atom offset tables (stco/co64) — a mistake
// there can corrupt the file outright, unlike FLAC (padding/metadata
// blocks are independent of the audio stream) or ID3v2 (the tag is a
// simple prefix block) — so it's left unsupported rather than risked; see
// ROADMAP.md.
package tagwriter

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrUnsupportedFormat is returned by Write for any file type other than
// MP3 or FLAC — see the package doc comment for why.
var ErrUnsupportedFormat = errors.New("tagwriter: unsupported format")

// Tags is the metadata Write embeds into a file — sourced from the
// matched database.Artist/Album/Track rows, not re-derived from the
// file's own (possibly wrong, that's the point) existing tags.
type Tags struct {
	Title       string
	Artist      string
	AlbumArtist string
	Album       string
	TrackNumber int
	DiscNumber  int
	Year        string

	// MusicBrainz IDs, written back so a future rescan recognizes this
	// file by direct MBID match (see internal/scanner's matcher)
	// immediately, without needing another fuzzy search.
	MusicBrainzArtistID       string
	MusicBrainzAlbumID        string // the release MBID
	MusicBrainzReleaseGroupID string
	MusicBrainzRecordingID    string
}

// Write embeds tags into the audio file at path, based on its extension.
// Returns ErrUnsupportedFormat for anything other than .mp3/.flac.
func Write(path string, tags Tags) error {
	switch extOf(path) {
	case "mp3":
		return writeID3v2(path, tags)
	case "flac":
		return writeFLACVorbisComment(path, tags)
	default:
		return ErrUnsupportedFormat
	}
}

// IsSupported reports whether Write can handle path's format — used by
// the API/UI to decide whether to offer a "Write tags" action at all,
// rather than letting the user hit ErrUnsupportedFormat after the fact.
func IsSupported(path string) bool {
	switch extOf(path) {
	case "mp3", "flac":
		return true
	default:
		return false
	}
}

func extOf(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}
