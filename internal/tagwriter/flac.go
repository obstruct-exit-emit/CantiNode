package tagwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"
)

// writeFLACVorbisComment updates (or creates) path's Vorbis comment
// metadata block with tags, preserving every other field already in
// there (GENRE, COMMENT, REPLAYGAIN_*, ...) and every other metadata
// block (STREAMINFO, embedded art, ...) untouched — only the specific
// fields tags sets are added or replaced.
func writeFLACVorbisComment(path string, tags Tags) error {
	// go-flac's own ParseFile opens path internally and only arranges for
	// it to be closed (via the *File.Close this function calls below) once
	// parsing succeeds — an error return here (a genuinely malformed FLAC
	// file slipping past the scanner's own read) leaks that file handle
	// inside the library with nothing on this side able to reach it. A
	// known upstream gap, not worth vendoring/patching the library over
	// for what should be a rare path (internal/scanner already read this
	// exact file successfully with dhowden/tag before ever matching it).
	f, err := flac.ParseFile(path)
	if err != nil {
		return fmt.Errorf("parse flac %s: %w", path, err)
	}

	cmt, idx := extractVorbisComment(f)
	if cmt == nil {
		cmt = flacvorbis.New()
	}

	setVorbisField(cmt, flacvorbis.FIELD_TITLE, tags.Title)
	setVorbisField(cmt, flacvorbis.FIELD_ARTIST, tags.Artist)
	setVorbisField(cmt, "ALBUMARTIST", tags.AlbumArtist)
	setVorbisField(cmt, flacvorbis.FIELD_ALBUM, tags.Album)
	if tags.TrackNumber > 0 {
		setVorbisField(cmt, flacvorbis.FIELD_TRACKNUMBER, strconv.Itoa(tags.TrackNumber))
	}
	if tags.DiscNumber > 0 {
		setVorbisField(cmt, "DISCNUMBER", strconv.Itoa(tags.DiscNumber))
	}
	setVorbisField(cmt, flacvorbis.FIELD_DATE, tags.Year)
	setVorbisField(cmt, "MUSICBRAINZ_ARTISTID", tags.MusicBrainzArtistID)
	setVorbisField(cmt, "MUSICBRAINZ_ALBUMID", tags.MusicBrainzAlbumID)
	setVorbisField(cmt, "MUSICBRAINZ_RELEASEGROUPID", tags.MusicBrainzReleaseGroupID)
	setVorbisField(cmt, "MUSICBRAINZ_TRACKID", tags.MusicBrainzRecordingID)

	block := cmt.Marshal()
	if idx >= 0 {
		f.Meta[idx] = &block
	} else {
		f.Meta = append(f.Meta, &block)
	}

	return saveFLAC(f, path)
}

// extractVorbisComment returns the file's existing Vorbis comment block
// (and its index in f.Meta) if it has one, or (nil, -1) otherwise.
func extractVorbisComment(f *flac.File) (*flacvorbis.MetaDataBlockVorbisComment, int) {
	for i, meta := range f.Meta {
		if meta.Type == flac.VorbisComment {
			cmt, err := flacvorbis.ParseFromMetaDataBlock(*meta)
			if err != nil {
				continue
			}
			return cmt, i
		}
	}
	return nil, -1
}

// setVorbisField removes every existing entry for key (Vorbis comment
// field names are case-insensitive) and, if value is non-empty, adds the
// new one — a no-op (field just removed, nothing re-added) for an empty
// value, e.g. an artist with no known album artist credit.
func setVorbisField(cmt *flacvorbis.MetaDataBlockVorbisComment, key, value string) {
	prefix := strings.ToUpper(key) + "="
	filtered := cmt.Comments[:0]
	for _, c := range cmt.Comments {
		if !strings.HasPrefix(strings.ToUpper(c), prefix) {
			filtered = append(filtered, c)
		}
	}
	cmt.Comments = filtered
	if value != "" {
		cmt.Add(key, value)
	}
}

// saveFLAC writes f to a temp file in path's directory and renames it
// over path — f must be fully written and closed before the rename,
// since a rename over a still-open file fails on Windows (this project
// develops on Windows day to day; see docs/development.md). f.WriteTo
// reads f.Frames, which ParseFile backs with path's own still-open file
// handle, so that read has to happen before f.Close(), and the rename
// has to happen after it.
func saveFLAC(f *flac.File, path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cantinode-tagwrite-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	if _, err := f.WriteTo(tmp); err != nil {
		tmp.Close()
		f.Close()
		return fmt.Errorf("write flac to temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		f.Close()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	cleanup = false
	return nil
}
