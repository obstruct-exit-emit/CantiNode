package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cantinode/cantinode/internal/database"
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
func FormatPath(format string, artist database.Artist, album database.Album, track database.Track, ext string) string {
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
func (s *Scanner) PlanOrganizePath(ctx context.Context, trackFileID int64) (string, error) {
	tf, err := s.db.GetTrackFile(ctx, trackFileID)
	if err != nil {
		return "", fmt.Errorf("get track file: %w", err)
	}
	if tf.TrackID == nil {
		return "", fmt.Errorf("track file %d is not matched, nothing to organize by", trackFileID)
	}

	track, err := s.db.GetTrack(ctx, *tf.TrackID)
	if err != nil {
		return "", fmt.Errorf("get track: %w", err)
	}
	album, err := s.db.GetAlbum(ctx, track.AlbumID)
	if err != nil {
		return "", fmt.Errorf("get album: %w", err)
	}
	artist, err := s.db.GetArtist(ctx, album.ArtistID)
	if err != nil {
		return "", fmt.Errorf("get artist: %w", err)
	}
	rootFolder, err := s.db.GetRootFolder(ctx, tf.RootFolderID)
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
func (s *Scanner) OrganizeFile(ctx context.Context, trackFileID int64) (string, error) {
	newPath, err := s.PlanOrganizePath(ctx, trackFileID)
	if err != nil {
		return "", err
	}

	tf, err := s.db.GetTrackFile(ctx, trackFileID)
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

	if err := s.db.SetTrackFileOrganized(ctx, trackFileID, newPath, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("record organized path: %w", err)
	}
	return newPath, nil
}
