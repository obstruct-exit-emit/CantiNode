// Package tagwriter writes corrected metadata back into an audio file's
// own tags, once internal/scanner has matched it to a MusicBrainz
// recording — the "fix the actual file, not just CantiNode's database"
// half of organizing a library.
//
// MP3 (ID3v2) and FLAC (Vorbis comments) are hand-rolled against their
// own well-understood, low-risk container formats. Everything else routes
// through go.senan.xyz/taglib (upstream TagLib compiled to WASM, run via
// wazero — no cgo) instead of being hand-rolled the same way: MP4/M4A in
// particular needs correctly rewriting nested atom offset tables
// (stco/co64) when the metadata atom's size changes, or the file's audio
// data silently points at the wrong bytes — a mistake there corrupts the
// file outright, unlike FLAC (metadata blocks are independent of the
// audio stream) or ID3v2 (the tag is a simple prefix block). Letting a
// mature, extensively-used library own that correctness beats re-deriving
// it from scratch.
//
// Every format tagreader can actually read tags from is covered — MP3,
// FLAC, the MP4/M4A family, OGG/Vorbis, Opus-in-Ogg, DSF, and WAV.
package tagwriter

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrUnsupportedFormat is returned by Write for any file type not listed
// in IsSupported — see the package doc comment for why the list stops
// where it does.
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
// Returns ErrUnsupportedFormat for any extension IsSupported doesn't list.
func Write(path string, tags Tags) error {
	switch extOf(path) {
	case "mp3":
		return writeID3v2(path, tags)
	case "flac":
		return writeFLACVorbisComment(path, tags)
	case "m4a", "m4b", "m4p", "ogg", "oga", "opus", "dsf", "wav":
		return writeTagLib(path, tags)
	default:
		return ErrUnsupportedFormat
	}
}

// IsSupported reports whether Write can handle path's format — used by
// the API/UI to decide whether to offer a "Write tags" action at all,
// rather than letting the user hit ErrUnsupportedFormat after the fact.
func IsSupported(path string) bool {
	switch extOf(path) {
	case "mp3", "flac", "m4a", "m4b", "m4p", "ogg", "oga", "opus", "dsf", "wav":
		return true
	default:
		return false
	}
}

func extOf(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}
