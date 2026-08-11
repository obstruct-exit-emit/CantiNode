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

// FormatPath renders format (e.g. Config.NamingFormat) against a matched
// file's artist/album/track, returning a path relative to its root
// folder. Every placeholder value is sanitized independently — format's
// own "/" separators (there to create subfolders) are left alone.
func FormatPath(format string, artist musiclibrary.Artist, album musiclibrary.Album, track musiclibrary.Track, ext string) string {
	year := "0000"
	if len(album.ReleaseDate) >= 4 {
		year = album.ReleaseDate[:4]
	}

	replacer := strings.NewReplacer(
		"{Artist}", sanitizePathComponent(artist.Name),
		"{Album}", sanitizePathComponent(album.Title),
		"{Year}", year,
		"{TrackNumber}", fmt.Sprintf("%02d", track.TrackNumber),
		"{DiscNumber}", strconv.Itoa(track.DiscNumber),
		"{Title}", sanitizePathComponent(track.Title),
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

	relPath := FormatPath(s.getNamingFormat(), *artist, *album, *track, filepath.Ext(tf.Path))
	return filepath.Join(rootFolder.Path, relPath), nil
}

// OrganizeFile moves trackFileID's file to its planned path (see
// PlanOrganizePath) and records the new path. A no-op (returns the
// current path, no error) if the file is already there. Refuses to
// overwrite an existing file at the destination — the caller finds out
// via the returned error rather than silently losing data.
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

	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return "", fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.Rename(tf.Path, newPath); err != nil {
		return "", fmt.Errorf("move %s to %s: %w", tf.Path, newPath, err)
	}

	if err := s.db.SetTrackFileOrganized(trackFileID, newPath, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("record organized path: %w", err)
	}
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
