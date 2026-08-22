// Package tagwriter writes corrected metadata back into an audio file's
// own tags, once internal/scanner has matched it to a MusicBrainz
// recording — the "fix the actual file, not just CantiNode's database"
// half of organizing a library.
//
// Every format routes through go.senan.xyz/taglib (upstream TagLib
// compiled to WASM, run via wazero — no cgo): MP4/M4A in particular needs
// correctly rewriting nested atom offset tables (stco/co64) when the
// metadata atom's size changes, or the file's audio data silently points
// at the wrong bytes. Letting a mature, extensively-used library own that
// correctness beats re-deriving it from scratch.
//
// MP3 and FLAC both used to be hand-rolled instead, on the reasoning that
// their tag formats are simple, low-risk containers (ID3v2 is a prefix
// block; Vorbis comment metadata blocks are independent of the audio
// stream) safe to build from scratch. MP3 moved to taglib first, after
// two real bugs surfaced live in the same week: the hand-rolled writer
// mislabeled non-ASCII text as ISO-8859-1 while writing raw UTF-8 bytes
// underneath (mojibake on any accented name), and it replaced the
// *entire* ID3v2 tag on every write, silently discarding any frame it
// didn't itself manage (embedded cover art, genre, comments...) — unlike
// every taglib-routed format, which only ever touches the specific
// fields being set. FLAC followed for consistency rather than a bug: its
// hand-rolled writer never had either problem (Vorbis comments have no
// encoding-byte ambiguity to get wrong, and it already merged instead of
// replacing), confirmed live before switching — genre, composer, and a
// seeded embedded-picture block all survived a taglib write untouched.
// The one confirmed behavior change is cosmetic: taglib normalizes a
// FLAC's padding metadata block to a fixed size on write, where the old
// writer left whatever padding was already there alone. TagLib writes
// both formats' MusicBrainz IDs using the same frame/field shapes Picard
// and other taggers use — the exact conventions tagreader.go already
// parses — so neither move was a behavior change for anything read back,
// only for what gets written.
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
	// Date is the release's full date (e.g. "2019-02-15"), not just a
	// 4-digit year — named Year in an earlier version of this struct, back
	// when the caller truncated it before ever reaching here.
	Date string
	// TrackTotal/DiscTotal are the release's own total counts (0 = unknown,
	// omitted rather than writing a literal "0") — distinct fields (not
	// baked into TrackNumber/DiscNumber as "N/M") since every other caller
	// of this struct's numeric fields expects a plain int, and TagLib's own
	// property mapping already reassembles TRACKTOTAL/DISCTOTAL into each
	// format's native combined representation where one exists (confirmed
	// live for ID3v2/MP3).
	TrackTotal int
	DiscTotal  int
	// Genre is a single string, not a list — a real recording can have
	// several, but writing them as taglib's own multi-value field came back
	// concatenated with no separator at all when read back from an MP3
	// (confirmed live), not the null-separated multi-value ID3v2 normally
	// uses. A caller with more than one genre joins them into one
	// unambiguous string (e.g. "Power Metal; Symphonic Metal") instead.
	Genre string
	// ReleaseType is the release group's MusicBrainz primary type (Album,
	// EP, Single, Compilation, ...).
	ReleaseType string
	// ArtistSortName/AlbumArtistSortName are the sort-name form of Artist/
	// AlbumArtist (e.g. "Beatles, The") — separate fields since the two
	// differ on a Various Artists compilation the same way Artist/
	// AlbumArtist themselves do.
	ArtistSortName      string
	AlbumArtistSortName string
	// ReleaseCountry/ReleaseStatus/Media describe the specific edition
	// matched (e.g. "US", "official", "2×CD") — sourced from whichever
	// release version cache already backs the album page's own edition
	// picker, so a caller with nothing cached for this exact release
	// simply leaves these blank rather than guessing.
	ReleaseCountry string
	ReleaseStatus  string
	Media          string
	// Mood is the album's own mood descriptor (e.g. "Trippy",
	// "Melancholic") — TheAudioDB's own field, cached alongside the
	// album's description.
	Mood string
	// CoverImage, when non-empty, is embedded as the file's front cover —
	// separate from every other field above since taglib writes an
	// embedded image through its own dedicated call, not the same
	// key-value tag map.
	CoverImage []byte

	// MusicBrainz IDs, written back so a future rescan recognizes this
	// file by direct MBID match (see internal/scanner's matcher)
	// immediately, without needing another fuzzy search.
	//
	// MusicBrainzArtistID is the recording's own real (primary) performer
	// — AlbumArtistID is the release's filing artist. The two differ on a
	// Various Artists compilation the same way Artist/AlbumArtist do; found
	// live that this file used to only ever write the filing artist's ID
	// under MusicBrainzArtistID, so a VA track's ARTIST tag correctly named
	// its real performer while the ID tag right next to it silently pointed
	// at Various Artists instead — the two disagreeing about whose
	// identity the frame even carries.
	MusicBrainzArtistID       string
	AlbumArtistID             string
	MusicBrainzAlbumID        string // the release MBID
	MusicBrainzReleaseGroupID string
	MusicBrainzRecordingID    string
}

// Write embeds tags into the audio file at path, based on its extension.
// Returns ErrUnsupportedFormat for any extension IsSupported doesn't list.
//
// clear controls whether fields this package doesn't itself manage
// (comments, lyrics, ReplayGain, an existing embedded picture other than
// what tags.CoverImage supplies, ...) are left alone (false — every other
// caller's existing expectation) or stripped outright (true — a
// deliberate, explicit "wipe first" pass, distinct from the accidental
// full-replace bug MP3's old hand-rolled writer used to have). false
// everywhere except a caller that specifically wants a clean-slate
// rewrite.
func Write(path string, tags Tags, clear bool) error {
	switch extOf(path) {
	case "mp3", "flac", "m4a", "m4b", "m4p", "ogg", "oga", "opus", "dsf", "wav":
		return writeTagLib(path, tags, clear)
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
