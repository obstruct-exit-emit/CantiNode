package organize

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/librinode/librinode/internal/scanner"
)

// Library-scope cleanup: beyond moving files into their template layout,
// organizing a library can also delete files that don't belong in it — junk
// left behind by downloads (.nfo/.txt/.torrent), or media of another type
// dumped into this library's root — and prune empty folders. Everything is
// previewed before deletion, and Apply re-validates each path: it must still
// sit inside one of THIS library's root folders and still be unwanted, so a
// stale or hand-edited request can never delete a wanted file. Files tracked
// in the database (matched or unmatched — the import flow's domain), the
// library's own media formats, sidecars (.opf), reader artwork (images), and
// ComicInfo.xml are always kept.

// Cleanup is one file organizing would delete: not this library's media, not
// a sidecar, not artwork, and not tracked in the database.
type Cleanup struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// keptEverywhere are non-media files every library wants to keep: metadata
// sidecars and reader artwork.
func keptEverywhere(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".opf", ".jpg", ".jpeg", ".png", ".webp", ".gif", ".xml":
		return true
	}
	return false
}

// mediaFor reports whether a filename is media the given library manages.
func mediaFor(mediaType, name string) bool {
	switch mediaType {
	case "ebook":
		return scanner.IsEbookPath(name)
	case "audiobook":
		return scanner.IsAudioPath(name)
	case "manga", "comic":
		return scanner.IsComicPath(name)
	case "magazine":
		return scanner.IsMagazinePath(name)
	}
	return true // unknown type: keep everything
}

// PlanCleanup walks one library's root folders and lists the files organizing
// would delete. Root-walk errors are reported as skips, not failures.
func (s *Service) PlanCleanup(mediaType string) ([]Cleanup, []string, error) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return nil, nil, err
	}

	cleanups := []Cleanup{}
	skips := []string{}
	for _, root := range roots {
		if root.MediaType != mediaType {
			continue
		}
		tracked, err := s.store.BookFilePathsUnderRoot(root.ID)
		if err != nil {
			return nil, nil, err
		}
		walkErr := filepath.WalkDir(root.Path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				skips = append(skips, fmt.Sprintf("%s: %v", path, err))
				if d != nil && d.IsDir() && path != root.Path {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && path != root.Path {
					return filepath.SkipDir
				}
				return nil
			}
			if _, isTracked := tracked[path]; isTracked {
				return nil
			}
			if mediaFor(mediaType, d.Name()) || keptEverywhere(d.Name()) {
				return nil
			}
			c := Cleanup{Path: path}
			if info, err := d.Info(); err == nil {
				c.Size = info.Size()
			}
			cleanups = append(cleanups, c)
			return nil
		})
		if walkErr != nil {
			skips = append(skips, fmt.Sprintf("%s: %v", root.Path, walkErr))
		}
	}
	sort.Slice(cleanups, func(i, j int) bool { return cleanups[i].Path < cleanups[j].Path })
	return cleanups, skips, nil
}

// ApplyCleanup deletes the requested unwanted files and prunes every empty
// directory under the library's roots. Each path is re-validated against the
// current state of disk and database — inside one of this library's roots,
// still not tracked, still not a wanted type — so nothing wanted can be
// deleted even by a stale or crafted request.
func (s *Service) ApplyCleanup(mediaType string, paths []string) (deleted int, pruned int, skips []string, err error) {
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return 0, 0, nil, err
	}
	type rootInfo struct {
		path    string
		tracked map[string]int64
	}
	mine := []rootInfo{}
	for _, root := range roots {
		if root.MediaType != mediaType {
			continue
		}
		tracked, terr := s.store.BookFilePathsUnderRoot(root.ID)
		if terr != nil {
			return 0, 0, nil, terr
		}
		mine = append(mine, rootInfo{path: filepath.Clean(root.Path), tracked: tracked})
	}

	skips = []string{}
	inMyRoot := func(path string) bool {
		for _, r := range mine {
			if strings.HasPrefix(path, r.path+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	isTracked := func(path string) bool {
		for _, r := range mine {
			if _, ok := r.tracked[path]; ok {
				return true
			}
		}
		return false
	}

	for _, p := range paths {
		p = filepath.Clean(p)
		switch {
		case !inMyRoot(p):
			skips = append(skips, fmt.Sprintf("%s: outside this library's root folders", p))
		case isTracked(p):
			skips = append(skips, fmt.Sprintf("%s: tracked in the library", p))
		case mediaFor(mediaType, p) || keptEverywhere(p):
			skips = append(skips, fmt.Sprintf("%s: a wanted file type", p))
		default:
			if rmErr := os.Remove(p); rmErr != nil {
				if !os.IsNotExist(rmErr) {
					skips = append(skips, fmt.Sprintf("%s: %v", p, rmErr))
				}
				continue
			}
			deleted++
		}
	}

	for _, r := range mine {
		pruned += pruneEmptyDirs(r.path)
	}
	if deleted > 0 || pruned > 0 {
		slog.Info("organize cleanup", "mediaType", mediaType, "deleted", deleted, "prunedDirs", pruned)
	}
	return deleted, pruned, skips, nil
}

// pruneEmptyDirs removes every empty directory under root (deepest first),
// never the root itself. Returns how many were removed.
func pruneEmptyDirs(root string) int {
	dirs := []string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // unreadable subtrees are simply left alone
		}
		if path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	// Deepest first so a chain of empty parents collapses in one pass.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	pruned := 0
	for _, d := range dirs {
		if err := os.Remove(d); err == nil { // fails on non-empty — the point
			pruned++
		}
	}
	return pruned
}
