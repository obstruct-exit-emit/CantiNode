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
	"strconv"
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
	AlbumArtistID             string `json:"albumArtistId,omitempty"` // the release's filing artist's own ID, distinct from MusicBrainzArtistID on a Various Artists track
	MusicBrainzAlbumID        string `json:"musicBrainzAlbumId"`      // the release MBID
	MusicBrainzReleaseGroupID string `json:"musicBrainzReleaseGroupId"`
	MusicBrainzRecordingID    string `json:"musicBrainzRecordingId"` // identifies this specific track/recording

	// Genre/ReleaseType/sort names/track-disc totals/release country-
	// status-media: the rest of what internal/tagwriter can write, read
	// back the same way — every field here empty just means the file
	// doesn't have it (or, for ArtistSortName/AlbumArtistSortName, that
	// this format's tagger of choice never wrote one; confirmed live that
	// go.senan.xyz/taglib doesn't write either for M4A/MP4 at all).
	Genre               string `json:"genre,omitempty"`
	ReleaseType         string `json:"releaseType,omitempty"`
	ArtistSortName      string `json:"artistSortName,omitempty"`
	AlbumArtistSortName string `json:"albumArtistSortName,omitempty"`
	TrackTotal          int    `json:"trackTotal,omitempty"`
	DiscTotal           int    `json:"discTotal,omitempty"`
	ReleaseCountry      string `json:"releaseCountry,omitempty"`
	ReleaseStatus       string `json:"releaseStatus,omitempty"`
	Media               string `json:"media,omitempty"`
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
		Genre:       m.Genre(),
		Format:      strings.ToLower(string(m.FileType())),
	}

	raw := extractRawTextFields(m.Raw())
	t.MusicBrainzArtistID = raw["musicbrainzartistid"]
	t.AlbumArtistID = raw["musicbrainzalbumartistid"]
	t.MusicBrainzAlbumID = raw["musicbrainzalbumid"]
	t.MusicBrainzReleaseGroupID = raw["musicbrainzreleasegroupid"]
	t.MusicBrainzRecordingID = raw["musicbrainztrackid"]
	// Vorbis comments use the short-form key directly; MP4 freeform atoms
	// and ID3v2 TXXX frames both use TagLib's older, MusicBrainz-prefixed
	// convention instead (confirmed live against a real write of each
	// format) — first non-empty alias wins.
	t.ReleaseType = firstNonEmpty(raw, "releasetype", "musicbrainzalbumtype")
	t.ReleaseCountry = firstNonEmpty(raw, "releasecountry", "musicbrainzalbumreleasecountry")
	t.ReleaseStatus = firstNonEmpty(raw, "releasestatus", "musicbrainzalbumstatus")
	// ID3v2 (MP3/DSF) exposes these under their own raw frame IDs
	// (TSOP/TSO2/TMED), not a human-readable name — dhowden/tag doesn't
	// translate them the way it does named TXXX/freeform fields.
	t.ArtistSortName = firstNonEmpty(raw, "artistsort", "tsop")
	t.AlbumArtistSortName = firstNonEmpty(raw, "albumartistsort", "tso2")
	t.Media = firstNonEmpty(raw, "media", "tmed")
	t.TrackTotal = atoiOrZero(raw["tracktotal"])
	t.DiscTotal = atoiOrZero(raw["disctotal"])

	return t
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// musicBrainzUFIDProvider is the well-known UFID owner identifier Picard
// (and most other taggers) use for the ID3v2 frame carrying a recording's
// MusicBrainz ID — the ID3v2 equivalent of Vorbis's MUSICBRAINZ_TRACKID.
const musicBrainzUFIDProvider = "http://musicbrainz.org"

// extractRawTextFields normalizes the several different shapes dhowden/tag
// hands back a text field in, keyed by format — not just MusicBrainz IDs
// despite the name's history; genre/release-type/sort-name/totals/country/
// status/media all flow through the same lookup:
//
//   - Vorbis comments (FLAC/OGG): plain lowercase keys, e.g.
//     "musicbrainz_artistid" -> string.
//   - ID3v2 (MP3/DSF): a NAMED custom field (MusicBrainz IDs, release
//     type/status/country) decodes to *tag.Comm{Description, Text} under
//     keys "TXXX", "TXXX_0", "TXXX_1", ...; a field with its own
//     dedicated ID3v2 frame (TSOP/TSO2/TMED — artist sort, album artist
//     sort, media) instead decodes straight to a string keyed by that raw
//     frame ID; the recording ID specifically comes from a UFID frame
//     (*tag.UFID{Provider, Identifier}) instead, since that's where
//     Picard actually writes it.
//   - MP4/M4A: iTunes freeform atoms decode straight to a string, keyed by
//     the atom's own name, e.g. "MusicBrainz Album Id" -> string.
//
// Normalizing every key (lowercase, strip spaces/underscores/hyphens)
// before matching collapses all of these into one lookup table instead of
// per-format code paths — callers needing a field that isn't reliably
// under one single normalized name across formats (confirmed live: e.g.
// ReleaseType is "releasetype" for Vorbis but "musicbrainzalbumtype" for
// ID3v2/MP4, which both use TagLib's older, MusicBrainz-prefixed naming
// convention instead of the modern short one) try each of that field's own
// known aliases via firstNonEmpty.
func extractRawTextFields(raw map[string]interface{}) map[string]string {
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
