// Package tagwriter writes corrected metadata back into an audio file's
// own tags, once internal/scanner has matched it to a MusicBrainz
// recording — the "fix the actual file, not just CantiNode's database"
// half of organizing a library.
//
// FLAC (Vorbis comments) is hand-rolled against its own well-understood,
// low-risk container format — metadata blocks are independent of the
// audio stream, so a mistake there can't corrupt playback. Every other
// format, MP3 included, routes through go.senan.xyz/taglib (upstream
// TagLib compiled to WASM, run via wazero — no cgo) instead: MP4/M4A in
// particular needs correctly rewriting nested atom offset tables
// (stco/co64) when the metadata atom's size changes, or the file's audio
// data silently points at the wrong bytes. Letting a mature,
// extensively-used library own that correctness beats re-deriving it from
// scratch.
//
// MP3 used to be hand-rolled too (ID3v2 is, in principle, as low-risk as
// Vorbis comments — the tag is a simple prefix block). It moved to
// taglib after two real bugs surfaced live in the same week: the
// hand-rolled writer mislabeled non-ASCII text as ISO-8859-1 while
// writing raw UTF-8 bytes underneath (mojibake on any accented name), and
// it replaced the *entire* ID3v2 tag on every write, silently discarding
// any frame it didn't itself manage (embedded cover art, genre,
// comments...) — unlike FLAC and every taglib-routed format, which only
// ever touch the specific fields being set. TagLib writes MP3's
// MusicBrainz IDs using the same UFID/TXXX-frame shapes Picard uses — the
// exact convention tagreader.go already parses — so this wasn't a
// behavior change for anything tagreader reads back, only a correctness
// fix for what gets written.
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
	case "flac":
		return writeFLACVorbisComment(path, tags)
	case "mp3", "m4a", "m4b", "m4p", "ogg", "oga", "opus", "dsf", "wav":
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
