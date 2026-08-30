package musicscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// illegalPathChars are characters not safely usable in a filename across
// the platforms CantiNode targets (Windows is the strictest: none of
// these are legal there, and avoiding them keeps a library portable to a
// Windows machine even when organized on Linux).
const illegalPathChars = `<>:"/\|?*`

// sanitizePathComponent replaces characters illegal in a filename with
// "_", so a metadata value (artist/album/track title) can never
// accidentally introduce a path separator or a Windows-illegal character
// into an organized filename.
func sanitizePathComponent(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(illegalPathChars, r) {
			return '_'
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// discNumberPlaceholder is {DiscNumber}'s own placeholder token — shared
// between FormatPath's substitution pass and stripDiscNumberSegment's
// detection pass so the two can never drift apart.
const discNumberPlaceholder = "{DiscNumber}"

// formatPlaceholders lists every placeholder FormatPath substitutes,
// {DiscNumber} included — stripDiscNumberSegment uses this to tell
// whether a segment containing {DiscNumber} is a dedicated disc folder
// (drop the whole segment) or shares the segment with something else
// essential (only drop the placeholder itself — see its own doc comment).
var formatPlaceholders = []string{
	"{Artist}", "{ArtistSortName}", "{Album}", "{ReleaseType}", "{Year}", "{Date}",
	"{TrackNumber}", discNumberPlaceholder, "{Title}", "{TrackArtist}", "{Ext}",
}

// stripDiscNumberSegment removes {DiscNumber} from format for a
// single-disc release when the "use disc number on single-disc
// releases" setting (config.NamingSettings.DisableDiscNumberForSingleDisc)
// is turned off — called by FormatPath's caller (see PlanOrganizePath)
// only once it already knows both that this release has just one disc
// and that the setting says not to use it there.
//
// {DiscNumber} alone in its own path segment (bounded by "/" on either
// side, or the start/end of format) — a dedicated "CD{DiscNumber}"
// folder, most commonly — has that whole segment dropped, so a
// single-disc release doesn't end up with a bare "CD" folder holding
// nothing. {DiscNumber} sharing a segment with any other placeholder
// (most commonly the filename itself, e.g.
// "{DiscNumber}-{TrackNumber} - {Title}") only has the placeholder
// itself removed — dropping the whole segment there would delete the
// filename along with it, which is never the intent. A format with no
// {DiscNumber} at all is returned unchanged.
func stripDiscNumberSegment(format string) string {
	segments := strings.Split(format, "/")
	kept := make([]string, 0, len(segments))
	for _, seg := range segments {
		if !strings.Contains(seg, discNumberPlaceholder) {
			kept = append(kept, seg)
			continue
		}
		dedicated := true
		for _, p := range formatPlaceholders {
			if p != discNumberPlaceholder && strings.Contains(seg, p) {
				dedicated = false
				break
			}
		}
		if dedicated {
			continue // drop the whole segment
		}
		kept = append(kept, strings.ReplaceAll(seg, discNumberPlaceholder, ""))
	}
	return strings.Join(kept, "/")
}

// FormatPath renders format (e.g. Config.NamingFormat) against a matched
// file's artist/album/track, returning a path relative to its root
// folder. Every placeholder value is sanitized independently — format's
// own "/" separators (there to create subfolders) are left alone.
// dropDiscSegment requests stripDiscNumberSegment's own treatment of
// {DiscNumber} first — the caller's decision, not FormatPath's, since it
// depends on whether this specific release has more than one disc, which
// FormatPath has no way to know from a single track alone.
func FormatPath(format string, artist musiclibrary.Artist, album musiclibrary.Album, track musiclibrary.Track, ext string, dropDiscSegment bool) string {
	if dropDiscSegment {
		format = stripDiscNumberSegment(format)
	}
	year := "0000"
	date := "0000-00-00"
	if len(album.ReleaseDate) >= 4 {
		year = album.ReleaseDate[:4]
		date = album.ReleaseDate
	}

	// trackArtist is the track's own real performer where it differs from
	// the album artist (the point of a Various Artists compilation) —
	// ArtistCredit is deliberately left empty by the matcher whenever it
	// would just repeat the album artist (see Track's own doc comment),
	// so falling back to artist.Name here reproduces exactly what the
	// track was actually credited to either way.
	trackArtist := track.ArtistCredit
	if trackArtist == "" {
		trackArtist = artist.Name
	}
	// sortName falls back the same way — an artist synced before
	// MusicBrainz's inc=... started requesting sort-name, or one with a
	// genuinely blank sort-name upstream, must still resolve to something
	// usable rather than collapsing this path component to empty.
	sortName := artist.SortName
	if sortName == "" {
		sortName = artist.Name
	}
	releaseType := album.PrimaryType
	if releaseType == "" {
		releaseType = "Album"
	}

	replacer := strings.NewReplacer(
		"{Artist}", sanitizePathComponent(artist.Name),
		"{ArtistSortName}", sanitizePathComponent(sortName),
		"{Album}", sanitizePathComponent(album.Title),
		"{ReleaseType}", sanitizePathComponent(releaseType),
		"{Year}", year,
		"{Date}", date,
		"{TrackNumber}", fmt.Sprintf("%02d", track.TrackNumber),
		discNumberPlaceholder, strconv.Itoa(track.DiscNumber),
		"{Title}", sanitizePathComponent(track.Title),
		"{TrackArtist}", sanitizePathComponent(trackArtist),
		"{Ext}", strings.TrimPrefix(ext, "."),
	)
	return filepath.FromSlash(replacer.Replace(format))
}

// PlanOrganizePath returns the absolute path trackFileID would move to
// under its own root folder, without moving anything — used both for a
// preview and as OrganizeFile's own first step. Returns an error if the
// file isn't matched (nothing to organize by) or its root folder/artist/
// album/track can't be loaded.
func (s *Scanner) PlanOrganizePath(trackFileID int64) (string, error) {
	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return "", fmt.Errorf("get track file: %w", err)
	}
	if tf.TrackID == nil {
		return "", fmt.Errorf("track file %d is not matched, nothing to organize by", trackFileID)
	}

	track, err := s.db.GetTrack(*tf.TrackID)
	if err != nil {
		return "", fmt.Errorf("get track: %w", err)
	}
	album, err := s.db.GetAlbum(track.AlbumID)
	if err != nil {
		return "", fmt.Errorf("get album: %w", err)
	}
	artist, err := s.db.GetArtist(album.ArtistID)
	if err != nil {
		return "", fmt.Errorf("get artist: %w", err)
	}
	rootFolder, err := s.db.GetRootFolder(tf.RootFolderID)
	if err != nil {
		return "", fmt.Errorf("get root folder: %w", err)
	}

	dropDiscSegment := false
	if s.getDisableDiscNumberForSingleDisc() {
		siblings, err := s.db.ListTracksByAlbum(album.ID)
		if err != nil {
			return "", fmt.Errorf("list tracks by album: %w", err)
		}
		singleDisc := true
		for _, sib := range siblings {
			if sib.DiscNumber > 1 {
				singleDisc = false
				break
			}
		}
		dropDiscSegment = singleDisc
	}

	relPath := FormatPath(s.getNamingFormat(), *artist, *album, *track, filepath.Ext(tf.Path), dropDiscSegment)
	return filepath.Join(rootFolder.Path, relPath), nil
}

// OrganizeFile moves trackFileID's file to its planned path (see
// PlanOrganizePath) and records the new path. A no-op (returns the
// current path, no error) if the file is already there. Refuses to
// overwrite an existing file at the destination — the caller finds out
// via the returned error rather than silently losing data. Sweeps up the
// file's old directory (and any now-empty ancestors, up to but never
// including the root folder itself) after a successful move — found live:
// this was documented ("Emptied folders are swept up...") but never
// actually implemented for Organize specifically, only for MoveArtist
// (see removeEmptyParents, mover.go) — a real multi-disc album organize
// left its entire old CD1/CD2 folder tree behind, empty but never
// cleaned up, once every file had moved out of it.
func (s *Scanner) OrganizeFile(trackFileID int64) (string, error) {
	newPath, err := s.PlanOrganizePath(trackFileID)
	if err != nil {
		return "", err
	}

	tf, err := s.db.GetTrackFile(trackFileID)
	if err != nil {
		return "", fmt.Errorf("get track file: %w", err)
	}
	if tf.Path == newPath {
		return newPath, nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return "", fmt.Errorf("destination already exists: %s", newPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat destination %s: %w", newPath, err)
	}
	rootFolder, err := s.db.GetRootFolder(tf.RootFolderID)
	if err != nil {
		return "", fmt.Errorf("get root folder: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}
	oldDir := filepath.Dir(tf.Path)
	if err := os.Rename(tf.Path, newPath); err != nil {
		return "", fmt.Errorf("move %s to %s: %w", tf.Path, newPath, err)
	}

	if err := s.db.SetTrackFileOrganized(trackFileID, newPath, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("record organized path: %w", err)
	}
	removeEmptyParents(oldDir, rootFolder.Path)
	return newPath, nil
}

// RenameMove is one track file's planned (or applied) move under
// organizing — the artist-level counterpart to the single-file preview/
// apply endpoints, which return just a bare destination path for the one
// file the caller already knows.
type RenameMove struct {
	TrackFileID int64  `json:"fileId"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// PlanOrganizeArtist previews the moves OrganizeArtist would make: every
// one of artistID's own track files (matched or manual — unmatched files
// have nothing to organize by, and are silently skipped rather than
// erroring the whole plan) whose planned path differs from its current
// one. A file already at its target path is left out entirely — an
// artist whose files already match the naming template gets an empty
// plan, not a list of no-op moves.
func (s *Scanner) PlanOrganizeArtist(artistID int64) ([]RenameMove, error) {
	files, err := s.db.ListTrackFilesByArtist(artistID)
	if err != nil {
		return nil, fmt.Errorf("list track files by artist: %w", err)
	}

	moves := []RenameMove{}
	for _, tf := range files {
		if tf.MatchStatus == musiclibrary.StatusUnmatched {
			continue
		}
		newPath, err := s.PlanOrganizePath(tf.ID)
		if err != nil {
			return nil, fmt.Errorf("plan organize %s: %w", tf.Path, err)
		}
		if newPath == tf.Path {
			continue
		}
		moves = append(moves, RenameMove{TrackFileID: tf.ID, From: tf.Path, To: newPath})
	}
	return moves, nil
}

// OrganizeArtist applies PlanOrganizeArtist's plan — see applyOrganizePlan
// for how per-file failures are handled.
func (s *Scanner) OrganizeArtist(artistID int64) (moves []RenameMove, errs []string, err error) {
	plan, err := s.PlanOrganizeArtist(artistID)
	if err != nil {
		return nil, nil, err
	}
	return s.applyOrganizePlan(plan)
}

// PlanOrganizeAlbum is PlanOrganizeArtist scoped to a single album — the
// album page's own Organize preview, which must never plan a move for a
// sibling album's files.
func (s *Scanner) PlanOrganizeAlbum(albumID int64) ([]RenameMove, error) {
	files, err := s.db.ListTrackFilesByAlbum(albumID)
	if err != nil {
		return nil, fmt.Errorf("list track files by album: %w", err)
	}

	moves := []RenameMove{}
	for _, tf := range files {
		if tf.MatchStatus == musiclibrary.StatusUnmatched {
			continue
		}
		newPath, err := s.PlanOrganizePath(tf.ID)
		if err != nil {
			return nil, fmt.Errorf("plan organize %s: %w", tf.Path, err)
		}
		if newPath == tf.Path {
			continue
		}
		moves = append(moves, RenameMove{TrackFileID: tf.ID, From: tf.Path, To: newPath})
	}
	return moves, nil
}

// OrganizeAlbum applies PlanOrganizeAlbum's plan — the album-scoped
// counterpart to OrganizeArtist.
func (s *Scanner) OrganizeAlbum(albumID int64) (moves []RenameMove, errs []string, err error) {
	plan, err := s.PlanOrganizeAlbum(albumID)
	if err != nil {
		return nil, nil, err
	}
	return s.applyOrganizePlan(plan)
}

// applyOrganizePlan moves each planned file one at a time via OrganizeFile —
// a failure moving one file is recorded in errs and does not stop the rest,
// the same non-aborting pattern ScanResult.Errors uses for a whole scan
// pass. moves holds only the files that actually moved successfully.
func (s *Scanner) applyOrganizePlan(plan []RenameMove) (moves []RenameMove, errs []string, err error) {
	moves = []RenameMove{}
	errs = []string{}
	for _, m := range plan {
		newPath, oerr := s.OrganizeFile(m.TrackFileID)
		if oerr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.From, oerr))
			continue
		}
		moves = append(moves, RenameMove{TrackFileID: m.TrackFileID, From: m.From, To: newPath})
	}
	return moves, errs, nil
}
