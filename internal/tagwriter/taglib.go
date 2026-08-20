package tagwriter

import (
	"strconv"

	taglib "go.senan.xyz/taglib"
)

// writeTagLib handles every format too risky or too complex to hand-roll
// safely the way writeFLACVorbisComment does — most importantly MP4/M4A,
// where the metadata atom sits before the audio data (mdat) and resizing
// it means correctly rewriting every track's stco/co64 chunk offset
// table, or the file's audio data silently points at the wrong bytes.
// go.senan.xyz/taglib wraps upstream TagLib (compiled to WASM, run via
// wazero — no cgo, matching the rest of this project) to get that
// correctness for free rather than re-deriving it under time pressure.
// Passing 0 (no WriteOption) merges these fields into the file's existing
// tags rather than replacing the whole tag set — matching
// writeFLACVorbisComment's own "leave everything else alone" behavior.
// MP3 also routes through here (not hand-rolled, despite ID3v2 being a
// low-risk container the same way FLAC is) after two real bugs in a
// hand-rolled writer surfaced live: mislabeling non-ASCII text as
// ISO-8859-1 while writing raw UTF-8 bytes underneath, and replacing the
// entire ID3v2 tag on every write instead of merging like every path
// here does. TagLib writes an MP3's MusicBrainz IDs as a UFID frame
// (MusicBrainzTrackID, owner "http://musicbrainz.org") plus TXXX frames
// for the rest — confirmed live against a real file — the same shapes
// Picard uses and tagreader.go already parses, so switching MP3 to this
// path changed nothing on the read side.
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
// else already in the file (GENRE, COMMENT, REPLAYGAIN_*, ...) — the same
// "leave everything else alone" behavior writeFLACVorbisComment's own
// setVorbisField already gives FLAC.
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
