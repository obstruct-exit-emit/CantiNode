package musicscanner

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
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
//
// When there's no separate artist-level folder, the album folder's own
// name gets one more try before giving up on Artist entirely: a single
// release folder named "Artist - Album" (a common torrent/scene naming
// convention — a whole release landing in one folder, never split into
// nested Artist/Album directories) is split accordingly. Only attempted
// when nothing above already supplied an artist, so a genuine nested
// Artist/Album structure is never second-guessed — and only on an exact
// two-part split, the same ambiguity guard artistAlbumFromFilename uses,
// since a real single-title album that happens to contain its own " - "
// (e.g. "Kind of Blue - Legacy Edition") is indistinguishable from this
// pattern by text alone; the match-confidence threshold downstream is
// what catches a wrong guess either way.
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
		return artist, album
	}

	if a, al, ok := splitArtistAlbum(album); ok {
		return a, al
	}
	return artist, album
}

// splitArtistAlbum splits a single folder name of the form "Artist -
// Album" into its two parts. Requires exactly one " - " occurrence — two
// or more (or none) is too ambiguous to confidently pick apart, so it's
// left as a single album name instead (see artistAlbumFromPath's own use
// — this is only tried once nothing else has already supplied an
// artist).
func splitArtistAlbum(name string) (artist, album string, ok bool) {
	parts := strings.Split(name, " - ")
	if len(parts) != 2 {
		return "", "", false
	}
	artist = strings.TrimSpace(parts[0])
	album = strings.TrimSpace(parts[1])
	if artist == "" || album == "" {
		return "", "", false
	}
	return artist, album, true
}

// filenameTrackNumberPattern matches a leading track number conventionally
// used by a tagless rip's bare filenames — "11 - 40'.flac", "01. Come on
// Home.flac", "03 Michael.flac", or a bare "11.flac" with no title at
// all — number first, then an optional separator-plus-title. The
// separator (one or more of space/dot/dash, run together as a single
// class rather than two competing alternatives — an earlier version of
// this split "- "/". "/" " into separate branches, which let the
// then-first-tried branch match just the single space before a dash in
// "11 - 40'", leaving a stray leading "- " stuck in the captured title;
// caught by this pattern's own test on first write) must still be
// present whenever a title follows — a filename that merely starts with
// digits for an unrelated reason ("128kbps Rip.flac", "2024
// Remaster.flac" glued straight to the next word) doesn't match at all,
// only one that actually punctuates the number off from the rest the way
// a real tracklist naming convention does. Capped at 3 digits — real
// track numbers are never more — so a bare 4-digit year prefix can't be
// misread as one either.
var filenameTrackNumberPattern = regexp.MustCompile(`^0*([0-9]{1,3})(?:[-.\s]+(.*))?$`)

// filenameTrackFallback derives a track-number/title guess from tf's own
// filename when its embedded tags have neither — the same "tags came up
// empty, try the filename" fallback resolveArtistAlbumFallback already
// gives Artist/Album, extended to the per-track slot itself. Found live: a
// completely tagless release (every file's TrackNumber/Title/Artist/Album
// all blank — confirmed real, not hypothetical, against an actual
// torrent) left every one of its files unmatched even once the whole
// folder's own release was confidently resolved via grab provenance,
// because slotTrack had nothing to go on at all for any individual file.
//
// Returns tags completely unchanged whenever TrackNumber is already
// set — an embedded tag is always trusted over a filename guess, never
// second-guessed — or the filename doesn't match
// filenameTrackNumberPattern's expected shape. The guessed title (when the
// embedded one is also blank) flows into slotTrack's own existing
// trackNumberSanityThreshold check exactly like a real embedded title
// would, so a filename-derived track number alone is never trusted any
// more blindly than an embedded one already is — the same flagrant-
// mismatch guard applies either way, not a new, weaker path around it.
func filenameTrackFallback(tags *tagreader.Tags, path string) *tagreader.Tags {
	if tags.TrackNumber > 0 {
		return tags
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	m := filenameTrackNumberPattern.FindStringSubmatch(name)
	if m == nil {
		return tags
	}
	num, err := strconv.Atoi(m[1])
	if err != nil || num <= 0 {
		return tags
	}
	guess := *tags
	guess.TrackNumber = num
	if guess.Title == "" {
		guess.Title = strings.TrimSpace(m[2])
	}
	return &guess
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
