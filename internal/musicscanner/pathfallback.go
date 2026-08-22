package musicscanner

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// resolveArtistAlbumFallback tries a file's own filename (if it encodes
// more than just the track title), then its containing folders, in that
// order — the shared last resort matchFileFuzzy and folderTagConsensus
// both reach for once a file's own tags come up empty on Artist and/or
// Album. Never touches tags themselves (kept as the source of truth for
// what the file actually has embedded — see TrackFileTagsModal); this is
// matching input only, computed fresh each time rather than cached
// anywhere.
func (s *Scanner) resolveArtistAlbumFallback(tf *musiclibrary.TrackFile) (artist, album string) {
	if fnArtist, fnAlbum := artistAlbumFromFilename(tf.Path); fnArtist != "" && fnAlbum != "" {
		return fnArtist, fnAlbum
	}
	return s.artistAlbumFromPath(tf)
}

// artistAlbumFromFilename pulls Artist/Album out of a bare filename that
// encodes more than just the track title — "Artist - Album - 01 -
// Title.ext" or "Artist - Album - Title.ext", the convention a file
// dropped straight into a root folder with no Artist/Album subfolders at
// all (or a stray loose file) is most likely to use. Requires at least
// three " - "-separated segments (Artist, Album, then a track number
// and/or title) before committing to a guess — a plain "Artist -
// Title.ext" two-segment name is too ambiguous to tell which segment is
// which, so it's deliberately left alone rather than mislabeling a title
// as an album or vice versa.
func artistAlbumFromFilename(path string) (artist, album string) {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.Split(name, " - ")
	if len(parts) < 3 {
		return "", ""
	}
	artist = cleanFolderName(parts[0])
	album = cleanFolderName(parts[1])
	if artist == "" || album == "" {
		return "", ""
	}
	return artist, album
}

// artistAlbumFromPath infers Artist/Album from tf's own containing
// directories — the album folder (tf's immediate parent) for Album, the
// artist folder (one level further up) for Artist. Stops at tf's own root
// folder boundary so the root folder's own name is never mistaken for an
// artist: a flat "RootFolder/Album/track.mp3" layout with no separate
// artist-level folder correctly returns an empty artist, not the root
// folder's own name (which could be anything — "Music", a drive label,
// unrelated to any real artist). Both return "" if the root folder lookup
// itself fails, or the corresponding folder level doesn't exist within
// the root folder.
func (s *Scanner) artistAlbumFromPath(tf *musiclibrary.TrackFile) (artist, album string) {
	rf, err := s.db.GetRootFolder(tf.RootFolderID)
	if err != nil {
		return "", ""
	}
	root := filepath.Clean(rf.Path)
	albumDir := filepath.Dir(filepath.Clean(tf.Path))
	if !isStrictlyWithin(root, albumDir) {
		return "", ""
	}
	album = cleanFolderName(filepath.Base(albumDir))

	artistDir := filepath.Dir(albumDir)
	if isStrictlyWithin(root, artistDir) {
		artist = cleanFolderName(filepath.Base(artistDir))
	}
	return artist, album
}

// isStrictlyWithin reports whether dir is a real descendant of root — not
// root itself, and not outside it (both cleaned, absolute paths expected).
func isStrictlyWithin(root, dir string) bool {
	return dir != root && strings.HasPrefix(dir, root+string(filepath.Separator))
}

// bracketedJunk matches a folder or filename segment's own bracketed/
// parenthesized annotations — [FLAC], (2019), {Deluxe Edition}, and the
// like — the kind of scene/quality/source tagging extremely common in a
// downloaded or ripped folder name but which would only dilute a
// MusicBrainz search's relevance ranking if left in.
var bracketedJunk = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)

// cleanFolderName turns a raw directory or filename segment into a
// plausible MusicBrainz search term — strips bracketed annotations, and
// folds any run of underscores/dots (both common word separators in a
// downloaded folder/file name, unlike a hyphen, which is often a
// meaningful part of a real title and so is left alone) down to a single
// space.
func cleanFolderName(name string) string {
	name = bracketedJunk.ReplaceAllString(name, " ")
	name = strings.Map(func(r rune) rune {
		switch r {
		case '_', '.':
			return ' '
		default:
			return r
		}
	}, name)
	return strings.Join(strings.Fields(name), " ")
}
