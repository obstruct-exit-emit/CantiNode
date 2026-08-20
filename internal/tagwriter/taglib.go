package tagwriter

import (
	"strconv"

	taglib "go.senan.xyz/taglib"
)

// writeTagLib is the only tag-writing implementation this package has —
// see the package doc comment for why MP3 and FLAC don't get their own
// hand-rolled paths despite being simple enough to make that tempting;
// MP4/M4A is the format that actually forces this dependency, needing
// correctly rewritten nested atom offset tables (stco/co64) when the
// metadata atom's size changes, or the file's audio data silently points
// at the wrong bytes. go.senan.xyz/taglib wraps upstream TagLib (compiled
// to WASM, run via wazero — no cgo, matching the rest of this project) to
// get that correctness for free rather than re-deriving it under time
// pressure. Passing 0 (no WriteOption) merges these fields into the
// file's existing tags rather than replacing the whole tag set, for
// every format this function handles — confirmed live for both MP3 (a
// GENRE/COMPOSER/embedded-art set survives a write untouched) and FLAC
// (same, plus a seeded METADATA_BLOCK_PICTURE).
func writeTagLib(path string, tags Tags) error {
	set := map[string][]string{}
	setField(set, taglib.Title, tags.Title)
	setField(set, taglib.Artist, tags.Artist)
	setField(set, taglib.AlbumArtist, tags.AlbumArtist)
	setField(set, taglib.Album, tags.Album)
	setIntField(set, taglib.TrackNumber, tags.TrackNumber)
	setIntField(set, taglib.DiscNumber, tags.DiscNumber)
	setField(set, taglib.Date, tags.Year)
	setField(set, taglib.MusicBrainzArtistID, tags.MusicBrainzArtistID)
	setField(set, taglib.MusicBrainzAlbumID, tags.MusicBrainzAlbumID)
	setField(set, taglib.MusicBrainzReleaseGroupID, tags.MusicBrainzReleaseGroupID)
	// "MusicBrainz Track Id" is the ecosystem-standard (if confusingly
	// named) tag for a recording's MBID — the same field Picard writes and
	// internal/tagreader already reads back as MusicBrainzRecordingID via
	// its own normalizeKey("MusicBrainz Track Id") == "musicbrainztrackid"
	// lookup, verified round-tripping correctly through dhowden/tag for
	// both M4A and OGG before this was wired in.
	setField(set, taglib.MusicBrainzTrackID, tags.MusicBrainzRecordingID)

	return taglib.WriteTags(path, set, 0)
}

// setField always sets key — an explicit empty slice (rather than simply
// omitting the key) clears any existing value for it instead of leaving a
// stale one behind, verified against a real file: WriteTags without the
// Clear option only touches keys present in the map, so a genuinely empty
// slice is how a single field gets cleared without disturbing anything
// else already in the file (GENRE, COMMENT, REPLAYGAIN_*, ...).
func setField(set map[string][]string, key, value string) {
	if value == "" {
		set[key] = []string{}
	} else {
		set[key] = []string{value}
	}
}

// setIntField is setField for a track/disc number — a value of 0 clears
// the field the same way an empty string does, rather than writing the
// literal "0".
func setIntField(set map[string][]string, key string, value int) {
	if value > 0 {
		setField(set, key, strconv.Itoa(value))
	} else {
		setField(set, key, "")
	}
}
