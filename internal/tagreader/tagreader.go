// Package tagreader reads embedded metadata (artist/album/track, and any
// MusicBrainz IDs already embedded by a ripper or Picard) out of an audio
// file. Most formats go through github.com/dhowden/tag (pure Go, no cgo);
// WAV is the one exception — dhowden/tag can open a WAV file but returns
// "no tags found" for every one tested, confirmed against a real file with
// genuine RIFF INFO tags — so it routes through go.senan.xyz/taglib
// instead (the same TagLib-via-WASM/wazero dependency internal/tagwriter
// already uses to write WAV's tags — see readTagLib).
//
// Audio properties (duration, bitrate) are not read here: dhowden/tag only
// parses metadata containers, not the audio stream itself, so decoding
// those accurately would need a real per-format audio decoder. Left as a
// known gap for a later pass (see ROADMAP.md) rather than faked.
package tagreader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dhowden/tag"
)

// Tags is the subset of an audio file's embedded metadata CantiNode's
// scanner and matcher care about. Also the wire shape returned directly by
// GET /api/v1/music/trackfile/{id}/tags and round-tripped through
// track_files.tags_json (internal/musicscanner writes it with
// json.Marshal, suggest.go reads it back with json.Unmarshal) — the
// explicit lowerCamelCase tags below match every other JSON type this API
// returns; encoding/json's case-insensitive fallback matching means an
// already-stored tags_json blob written before these tags existed still
// unmarshals correctly.
type Tags struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	AlbumArtist string `json:"albumArtist"`
	Album       string `json:"album"`
	TrackNumber int    `json:"trackNumber"`
	DiscNumber  int    `json:"discNumber"`
	Year        int    `json:"year"`
	// Format is the detected file type, lowercased (e.g. "mp3", "flac",
	// "m4a") — dhowden/tag detects this from file content, not extension.
	Format string `json:"format"`

	// MusicBrainz IDs, populated only when the file's own tags already
	// carry them (common — Picard and most rippers embed these). Empty
	// when absent; internal/scanner falls back to a fuzzy MusicBrainz
	// search in that case.
	MusicBrainzArtistID       string `json:"musicBrainzArtistId"`
	MusicBrainzAlbumID        string `json:"musicBrainzAlbumId"` // the release MBID
	MusicBrainzReleaseGroupID string `json:"musicBrainzReleaseGroupId"`
	MusicBrainzRecordingID    string `json:"musicBrainzRecordingId"` // identifies this specific track/recording
}

// audioExtensions are the file extensions worth attempting to read — a
// fast pre-filter for the scanner's directory walk, covering every format
// either backend actually reads tags from: MP3, MP4/M4A family, FLAC,
// OGG/Vorbis, Opus-in-Ogg (dhowden/tag detects the actual container
// format from file content and, despite this package's own doc comment
// once claiming otherwise, does correctly parse OpusTags — it treats it
// as an alias of the byte-identical VorbisComment structure; see ogg.go's
// opusTagsPrefix), and DSF via dhowden/tag; WAV via go.senan.xyz/taglib.
// Not WMA — neither backend reads it.
var audioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".m4b": true, ".m4p": true,
	".ogg": true, ".oga": true, ".opus": true, ".dsf": true, ".wav": true,
}

// IsAudioFile reports whether path's extension is one tagreader can read.
func IsAudioFile(path string) bool {
	return audioExtensions[strings.ToLower(filepath.Ext(path))]
}

// Read parses the audio file at path and returns its tags.
func Read(path string) (*Tags, error) {
	if extOf(path) == "wav" {
		return readTagLib(path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return nil, fmt.Errorf("read tags from %s: %w", path, err)
	}
	t := fromMetadata(m)
	if t.Format == "" {
		// dhowden/tag's MP4 reader (mp4.go) never actually assigns
		// metadataMP4.fileType — it's initialized to UnknownFileType
		// ("") in ReadAtoms and nothing afterward ever sets it, so
		// FileType() always returns "" for every M4A/M4B/M4P file,
		// confirmed directly against the pinned dependency source and
		// empirically against a real fixture. That empty string was
		// getting stored verbatim as this file's format (in the
		// track_files DB row, and from there into every UI that reads
		// it, e.g. the album page's write-tags button gate, which used
		// it to decide the format was unsupported). Falling back to the
		// extension only when content-detection came back with nothing
		// keeps the original resilience for every format dhowden/tag
		// DOES detect (a wrong extension on an MP3/FLAC/OGG/DSF file
		// still reports its true, sniffed format, not a guess).
		t.Format = extOf(path)
	}
	return t, nil
}

func extOf(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}

func fromMetadata(m tag.Metadata) *Tags {
	trackNum, _ := m.Track()
	discNum, _ := m.Disc()

	t := &Tags{
		Title:       m.Title(),
		Artist:      m.Artist(),
		AlbumArtist: m.AlbumArtist(),
		Album:       m.Album(),
		TrackNumber: trackNum,
		DiscNumber:  discNum,
		Year:        m.Year(),
		Format:      strings.ToLower(string(m.FileType())),
	}

	mbids := extractMusicBrainzIDs(m.Raw())
	t.MusicBrainzArtistID = mbids["musicbrainzartistid"]
	t.MusicBrainzAlbumID = mbids["musicbrainzalbumid"]
	t.MusicBrainzReleaseGroupID = mbids["musicbrainzreleasegroupid"]
	t.MusicBrainzRecordingID = mbids["musicbrainztrackid"]

	return t
}

// musicBrainzUFIDProvider is the well-known UFID owner identifier Picard
// (and most other taggers) use for the ID3v2 frame carrying a recording's
// MusicBrainz ID — the ID3v2 equivalent of Vorbis's MUSICBRAINZ_TRACKID.
const musicBrainzUFIDProvider = "http://musicbrainz.org"

// extractMusicBrainzIDs normalizes the three different shapes dhowden/tag
// hands back MusicBrainz identifiers in, keyed by format:
//
//   - Vorbis comments (FLAC/OGG): plain lowercase keys, e.g.
//     "musicbrainz_artistid" -> string.
//   - ID3v2 (MP3): custom TXXX frames decode to *tag.Comm{Description,
//     Text} under keys "TXXX", "TXXX_0", "TXXX_1", ...; the recording ID
//     specifically comes from a UFID frame (*tag.UFID{Provider,
//     Identifier}) instead, since that's where Picard actually writes it.
//   - MP4/M4A: iTunes freeform atoms decode straight to a string, keyed by
//     the atom's own name, e.g. "MusicBrainz Album Id" -> string.
//
// Normalizing every key (lowercase, strip spaces/underscores/hyphens)
// before matching collapses all three into one lookup table instead of
// three format-specific code paths.
func extractMusicBrainzIDs(raw map[string]interface{}) map[string]string {
	out := map[string]string{}
	for k, v := range raw {
		switch val := v.(type) {
		case string:
			out[normalizeKey(k)] = trimMP4FreeformPadding(val)
		case *tag.Comm:
			out[normalizeKey(val.Description)] = val.Text
		case *tag.UFID:
			if val.Provider == musicBrainzUFIDProvider {
				out["musicbrainztrackid"] = string(val.Identifier)
			}
		}
	}
	return out
}

// trimMP4FreeformPadding works around a real bug in dhowden/tag's MP4
// atom parser (readCustomAtom in mp4.go): an iTunes freeform ("----")
// atom's "data" sub-atom has an 8-byte header (4-byte type indicator +
// 4-byte locale, both typically zero) before its actual text content, but
// readCustomAtom only skips 4 of those 8 bytes — so every freeform value
// (which is exactly how a custom tag like "MusicBrainz Album Id" is
// stored on MP4/M4A) comes back with 4 leading NUL bytes still attached,
// confirmed against a real file written by go.senan.xyz/taglib (see
// internal/tagwriter). Harmless no-op for every other format this
// function also handles (Vorbis comments are plain UTF-8 text with no
// legitimate reason to ever start with a NUL byte), so trimming
// unconditionally here — rather than only for MP4 specifically — needs no
// format check.
func trimMP4FreeformPadding(s string) string {
	return strings.TrimLeft(s, "\x00")
}

func normalizeKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == '_' || r == ' ' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
