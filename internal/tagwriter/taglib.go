package tagwriter

import (
	"fmt"
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
// (same, plus a seeded METADATA_BLOCK_PICTURE). clear passes taglib.Clear
// instead, an explicit opt-in for a caller that wants a clean slate — see
// Write's own doc comment.
func writeTagLib(path string, tags Tags, clear bool) error {
	set := map[string][]string{}
	setField(set, taglib.Title, tags.Title)
	setField(set, taglib.Artist, tags.Artist)
	setField(set, taglib.AlbumArtist, tags.AlbumArtist)
	setField(set, taglib.Album, tags.Album)
	setIntField(set, taglib.TrackNumber, tags.TrackNumber)
	setIntField(set, taglib.DiscNumber, tags.DiscNumber)
	setIntField(set, "TRACKTOTAL", tags.TrackTotal)
	setIntField(set, "DISCTOTAL", tags.DiscTotal)
	setField(set, taglib.Date, tags.Date)
	// Genre/ReleaseType/sort names use setFieldIfPresent, not setField —
	// found live: these are best-effort supplementary data (Genre in
	// particular comes from Artist.Genres, a cache that may genuinely
	// never have been fetched yet for a given artist), unlike Title/
	// Artist/Album/the MusicBrainz IDs, which are always authoritatively
	// resolved (or genuinely absent) the moment a file is matched. Blank
	// here means "CantiNode has no opinion," not "the correct value is
	// blank" — clearing a file's existing GENRE just because CantiNode
	// hasn't cached one yet would silently destroy real, possibly
	// hand-curated data for no reason. Confirmed live: this exact
	// difference broke every "preserves untracked fields" test the moment
	// these fields were added with plain setField.
	setFieldIfPresent(set, taglib.Genre, tags.Genre)
	setFieldIfPresent(set, taglib.ReleaseType, tags.ReleaseType)
	setFieldIfPresent(set, taglib.ArtistSort, tags.ArtistSortName)
	setFieldIfPresent(set, taglib.AlbumArtistSort, tags.AlbumArtistSortName)
	// Same reasoning as Genre/ReleaseType/sort names above: only cached
	// once the release version picker's own cache has actually been
	// fetched for this release group, so blank means "nothing cached yet,"
	// not "there's genuinely no country/status/media."
	setFieldIfPresent(set, taglib.ReleaseCountry, tags.ReleaseCountry)
	setFieldIfPresent(set, taglib.ReleaseStatus, tags.ReleaseStatus)
	setFieldIfPresent(set, taglib.Media, tags.Media)
	setFieldIfPresent(set, taglib.Mood, tags.Mood)
	setFieldIfPresent(set, taglib.Composer, tags.Composer)
	setField(set, taglib.MusicBrainzArtistID, tags.MusicBrainzArtistID)
	setField(set, taglib.MusicBrainzAlbumArtistID, tags.AlbumArtistID)
	setField(set, taglib.MusicBrainzAlbumID, tags.MusicBrainzAlbumID)
	setField(set, taglib.MusicBrainzReleaseGroupID, tags.MusicBrainzReleaseGroupID)
	// "MusicBrainz Track Id" is the ecosystem-standard (if confusingly
	// named) tag for a recording's MBID — the same field Picard writes and
	// internal/tagreader already reads back as MusicBrainzRecordingID via
	// its own normalizeKey("MusicBrainz Track Id") == "musicbrainztrackid"
	// lookup, verified round-tripping correctly through dhowden/tag for
	// both M4A and OGG before this was wired in.
	setField(set, taglib.MusicBrainzTrackID, tags.MusicBrainzRecordingID)

	var opts taglib.WriteOption
	if clear {
		opts = taglib.Clear
	}
	if err := taglib.WriteTags(path, set, opts); err != nil {
		return err
	}

	// A separate call: embedded images are their own concept in TagLib,
	// not part of the key-value tag map above, so Clear above never
	// touches them either way. Writing at index 0 ("Front Cover")
	// overwrites whatever was already there — deliberately unconditional
	// on clear, since embedding real cover art is worth doing on an
	// ordinary merge-mode write too, not just a clean-slate one.
	if len(tags.CoverImage) > 0 {
		if err := taglib.WriteImage(path, tags.CoverImage); err != nil {
			return fmt.Errorf("write cover image: %w", err)
		}
	}
	return nil
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

// setFieldIfPresent omits key entirely when value is empty, rather than
// setField's own always-set-even-to-clear behavior — see writeTagLib's
// call site for which fields need this and why.
func setFieldIfPresent(set map[string][]string, key, value string) {
	if value != "" {
		set[key] = []string{value}
	}
}
