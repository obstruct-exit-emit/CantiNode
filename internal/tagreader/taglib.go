package tagreader

import (
	"fmt"
	"strconv"
	"strings"

	taglib "go.senan.xyz/taglib"
)

// readTagLib is Read's WAV path — dhowden/tag can open a WAV file without
// erroring, but returns "no tags found" for every one tested, confirmed
// against a real file with genuine RIFF INFO tags (see the package doc
// comment). go.senan.xyz/taglib (upstream TagLib via WASM/wazero — no
// cgo, the same dependency internal/tagwriter already uses to write WAV's
// tags) reads them correctly.
func readTagLib(path string) (*Tags, error) {
	raw, err := taglib.ReadTags(path)
	if err != nil {
		return nil, fmt.Errorf("read tags from %s: %w", path, err)
	}

	// Properties is only consulted for Format — best-effort, since a
	// failure here says nothing about whether the tags themselves (already
	// read above) are trustworthy.
	format := extOf(path)
	if props, err := taglib.ReadProperties(path); err == nil && props.Format != "" {
		format = strings.ToLower(props.Format)
	}

	return &Tags{
		Title:                     firstTag(raw, taglib.Title),
		Artist:                    firstTag(raw, taglib.Artist),
		AlbumArtist:               firstTag(raw, taglib.AlbumArtist),
		Album:                     firstTag(raw, taglib.Album),
		TrackNumber:               leadingInt(firstTag(raw, taglib.TrackNumber)),
		DiscNumber:                leadingInt(firstTag(raw, taglib.DiscNumber)),
		Year:                      leadingInt(firstTag(raw, taglib.Date)),
		Format:                    format,
		MusicBrainzArtistID:       firstTag(raw, taglib.MusicBrainzArtistID),
		MusicBrainzAlbumID:        firstTag(raw, taglib.MusicBrainzAlbumID),
		MusicBrainzReleaseGroupID: firstTag(raw, taglib.MusicBrainzReleaseGroupID),
		// "MusicBrainz Track Id" is the ecosystem-standard (if confusingly
		// named) tag for a recording's MBID — see internal/tagwriter's own
		// use of the same taglib.MusicBrainzTrackID constant when writing.
		MusicBrainzRecordingID: firstTag(raw, taglib.MusicBrainzTrackID),
	}, nil
}

func firstTag(raw map[string][]string, key string) string {
	if v := raw[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// leadingInt parses a track/disc/year value that may be a bare number
// ("5"), an "N/total" pair ("5/12", dhowden/tag's own Track()/Disc()
// convention for the other formats), or a full date ("2019-03-15") — the
// leading run of digits is all any of these formats need here.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
