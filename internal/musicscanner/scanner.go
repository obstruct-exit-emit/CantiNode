// Package musicscanner walks CantiNode's music root folders, reads each
// audio file's tags (internal/tagreader), matches it against MusicBrainz
// (internal/musicbrainz), and organizes matched files on disk into a
// consistent layout. Ported from CantiNode's own original, from-scratch
// scanner package (before this codebase was rebuilt on top of a fork of
// LibriNode), whose parse-filename-then-lookup-metadata-provider matching
// model didn't fit the audio-tag/whole-album matching this domain needs.
package musicscanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cantinode/cantinode/internal/coverart"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/tagreader"
	"github.com/cantinode/cantinode/internal/tagwriter"
)

// Scanner ties the database, MusicBrainz client, and tag reader together
// into the scan -> match -> organize pipeline.
type Scanner struct {
	db       *musiclibrary.Store
	mb       *musicbrainz.Client
	coverart *coverart.Client
	logger   *slog.Logger

	// settingsMu guards namingFormat/minMatchConfidence/organizeOnMatch/
	// tagToggles — internal/api's settings endpoint calls UpdateSettings
	// from an HTTP handler goroutine while a scan (reading them from
	// ScanRootFolder's own goroutine) may be in flight, so a plain
	// unsynchronized field would be a real data race, not just a style
	// nitpick.
	settingsMu         sync.RWMutex
	namingFormat       string
	minMatchConfidence float64
	organizeOnMatch    bool
	tagToggles         tagwriter.Toggles
}

// New returns a Scanner. namingFormat/minMatchConfidence/organizeOnMatch
// mirror the equivalent config.Config fields (kept as plain parameters
// here rather than a *config.Config dependency, so this package doesn't
// need to import internal/config just to read three values) — see
// UpdateSettings to change them after construction. coverartClient may be
// nil (tests, or any setup that doesn't want embedded art) — WriteTags
// simply never embeds a cover in that case, same as it never fetching one
// at all. tagToggles gates which tagwriter.Tags fields WriteTags actually
// writes — pass tagwriter.AllEnabled for a caller (most tests) that
// doesn't care about per-field settings.
func New(db *musiclibrary.Store, mb *musicbrainz.Client, coverartClient *coverart.Client, logger *slog.Logger, namingFormat string, minMatchConfidence float64, organizeOnMatch bool, tagToggles tagwriter.Toggles) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		db:                 db,
		mb:                 mb,
		coverart:           coverartClient,
		logger:             logger,
		namingFormat:       namingFormat,
		minMatchConfidence: minMatchConfidence,
		organizeOnMatch:    organizeOnMatch,
		tagToggles:         tagToggles,
	}
}

// UpdateSettings applies a live settings change (from internal/api's
// PUT /api/v1/settings) to this Scanner — takes effect on the very next
// file scanned/organized/tag-written, no restart needed.
func (s *Scanner) UpdateSettings(namingFormat string, minMatchConfidence float64, organizeOnMatch bool, tagToggles tagwriter.Toggles) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.namingFormat = namingFormat
	s.minMatchConfidence = minMatchConfidence
	s.organizeOnMatch = organizeOnMatch
	s.tagToggles = tagToggles
}

func (s *Scanner) getNamingFormat() string {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.namingFormat
}

func (s *Scanner) getMinMatchConfidence() float64 {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.minMatchConfidence
}

func (s *Scanner) getOrganizeOnMatch() bool {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.organizeOnMatch
}

func (s *Scanner) getTagToggles() tagwriter.Toggles {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.tagToggles
}

// ScanResult summarizes one scan pass (either a single root folder or
// every root folder), for the API/UI to report on.
type ScanResult struct {
	FilesFound     int      `json:"filesFound"`
	FilesMatched   int      `json:"filesMatched"`
	FilesOrganized int      `json:"filesOrganized"`
	FilesRemoved   int      `json:"filesRemoved"` // rows deleted because the file is no longer on disk
	Errors         []string `json:"errors"`
}

func (r *ScanResult) merge(other ScanResult) {
	r.FilesFound += other.FilesFound
	r.FilesMatched += other.FilesMatched
	r.FilesOrganized += other.FilesOrganized
	r.FilesRemoved += other.FilesRemoved
	r.Errors = append(r.Errors, other.Errors...)
}

// ScanAll scans every configured root folder in turn. One root folder
// failing (most commonly: a network-mounted root that's transiently
// unreachable — see ScanRootFolder's own doc comment) is recorded in
// result.Errors and does not stop the rest — a library with several root
// folders shouldn't have its always-available local ones go unscanned
// just because one network share happened to be down at the time.
func (s *Scanner) ScanAll(ctx context.Context) (*ScanResult, error) {
	folders, err := s.db.ListRootFolders()
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}

	// Errors initialized non-nil so it JSON-encodes to [] rather than
	// null when empty — internal/api returns a ScanResult straight
	// through to the frontend.
	result := &ScanResult{Errors: []string{}}
	for _, rf := range folders {
		r, err := s.ScanRootFolder(ctx, rf)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("root folder %s: %v", rf.Path, err))
			continue
		}
		result.merge(*r)
	}
	return result, nil
}

// ScanRootFolder walks rf.Path for audio files, upserts a track_files row
// per file found, then matches every not-yet-matched file, and removes
// rows for files no longer present on disk. A per-file read or match
// error is recorded in the result and does not stop the scan.
//
// Matching happens in two passes, not inline during the walk: every file
// is upserted first (result.FilesFound bookkeeping, and a directory can't
// be grouped until its files all have rows), then not-yet-matched files
// are grouped by directory (filepath.Dir) and matched together, one
// folder at a time — see matchFolder. This is CantiNode's whole-album
// matching: files from the same folder converge on one MusicBrainz
// release instead of each independently guessing, which is what used to
// let a single album folder split across several different releases.
func (s *Scanner) ScanRootFolder(ctx context.Context, rf musiclibrary.RootFolder) (*ScanResult, error) {
	// Errors initialized non-nil so it JSON-encodes to [] rather than
	// null when empty — internal/api returns a ScanResult straight
	// through to the frontend.
	result := &ScanResult{Errors: []string{}}

	// Checked up front, before the walk even starts: a root folder can be
	// a network mount (CIFS/NFS/etc.) that's transiently unreachable — a
	// NAS reboot, a network blip, a share not yet re-established after a
	// host reboot. filepath.WalkDir's callback below is invoked with a
	// non-nil err for the root itself when that happens; returning nil
	// from it (to keep the walk resilient to a single bad subfolder deep
	// inside a tree that's otherwise fine — see the callback below) makes
	// WalkDir return successfully overall having visited nothing at all.
	// Proceeding to DeleteTrackFilesMissing with an empty seenPaths in
	// that case would prune every track_files row for this root folder,
	// even though every file is still physically present and only the
	// mount is briefly down. Bailing out here — before any walking or
	// pruning — is what actually prevents that; distinguishing "root
	// inaccessible" from "some individual file/subfolder inside it is
	// unreadable" from inside the callback would be far more fragile.
	if info, err := os.Stat(rf.Path); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return result, fmt.Errorf("root folder %s is not accessible, refusing to scan (would wrongly prune every tracked file): %w", rf.Path, err)
	}

	var seenPaths []string
	groups := map[string][]folderEntry{}

	err := filepath.WalkDir(rf.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("walk %s: %v", path, err))
			return nil
		}
		if d.IsDir() || !tagreader.IsAudioFile(path) {
			return nil
		}
		seenPaths = append(seenPaths, path)

		tf, tags, err := s.upsertFile(rf, path, result)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if tf.MatchStatus != musiclibrary.StatusUnmatched {
			// Already matched (auto or manual) by a previous scan — never
			// silently re-decided here, and excluded from folder-grouping
			// entirely. A user who wants to redo a match uses the
			// manual-match API explicitly.
			return nil
		}
		dir := filepath.Dir(path)
		groups[dir] = append(groups[dir], folderEntry{tf: tf, tags: tags})
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("walk %s: %w", rf.Path, err)
	}

	// Merge CD1/CD2/Disc-N sibling subfolders of the same multi-disc album
	// into one logical group before matching — see groupMultiDiscFolders.
	// Purely an in-memory regrouping for matching purposes; files stay
	// exactly where they are on disk until an explicit Organize action.
	groups = groupMultiDiscFolders(groups)

	// Sorted for deterministic scan behavior/logging, not correctness —
	// map iteration order would otherwise vary run to run.
	dirs := make([]string, 0, len(groups))
	for dir := range groups {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		s.matchFolder(ctx, groups[dir], result)
	}

	removed, err := s.db.DeleteTrackFilesMissing(rf.ID, seenPaths)
	if err != nil {
		return result, fmt.Errorf("prune missing files: %w", err)
	}
	result.FilesRemoved = int(removed)

	return result, nil
}

// ScanAlbumFolder rescans a single album's own folder for new or changed
// audio files and matches them against this album's own tracks — the album
// page's "Scan files" action, scoped to just this album's directory rather
// than every root folder. The directory scanned is the common ancestor of
// ALL of the album's existing track files' directories (there's no other
// reliable way to know where an album's files live), so this errors on an
// album with none yet. Using every existing file rather than just the
// first one matters for a multi-disc album: existing[0] could land in
// either CD1 or CD2, and scanning only that one disc's subfolder would
// silently never discover new/changed files sitting in the other disc's
// folder — the same sibling-folder walk ScanRootFolder already does for a
// full library scan.
//
// Never runs DeleteTrackFilesMissing (which reconciles a whole root folder
// against seenPaths — doing that here with a directory-scoped seenPaths
// list would wrongly delete every OTHER album's rows under the same root
// folder). It does still prune this album's own already-known files if
// they're simply gone — e.g. the folder was deleted outside the app
// entirely, which the walk below can't discover an absence of on its own
// (WalkDir on a missing directory reports one error for the root path and
// stops, silently leaving stale rows behind otherwise).
func (s *Scanner) ScanAlbumFolder(ctx context.Context, albumID int64) (*ScanResult, error) {
	existing, err := s.db.ListTrackFilesByAlbum(albumID)
	if err != nil {
		return nil, fmt.Errorf("list track files by album: %w", err)
	}
	if len(existing) == 0 {
		return nil, fmt.Errorf("album has no files yet to locate its folder from")
	}
	rootFolder, err := s.db.GetRootFolder(existing[0].RootFolderID)
	if err != nil {
		return nil, fmt.Errorf("get root folder: %w", err)
	}
	fileDirs := make([]string, 0, len(existing))
	for _, tf := range existing {
		fileDirs = append(fileDirs, filepath.Dir(tf.Path))
	}
	dir := commonAncestorDir(fileDirs)

	result := &ScanResult{Errors: []string{}}
	groups := map[string][]folderEntry{}
	seen := map[string]bool{}

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("walk %s: %v", path, err))
			return nil
		}
		if d.IsDir() || !tagreader.IsAudioFile(path) {
			return nil
		}
		seen[path] = true
		tf, tags, err := s.upsertFile(*rootFolder, path, result)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		if tf.MatchStatus != musiclibrary.StatusUnmatched {
			return nil
		}
		fdir := filepath.Dir(path)
		groups[fdir] = append(groups[fdir], folderEntry{tf: tf, tags: tags})
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("walk %s: %w", dir, err)
	}

	// Prune this album's own files the walk didn't find — scoped to exactly
	// the files it already owned (existing), never a whole directory, so a
	// sibling album's rows are never at risk even without
	// DeleteTrackFilesMissing's whole-root-folder context here.
	for _, tf := range existing {
		if seen[tf.Path] {
			continue
		}
		_, statErr := os.Stat(tf.Path)
		if statErr == nil {
			continue // still there, just outside the walked dir somehow
		}
		if !os.IsNotExist(statErr) {
			// A permission error, a temporarily-disconnected network mount,
			// an AV lock, etc. isn't proof the file is gone — only a
			// confirmed "not found" is. Report it and leave the row alone
			// rather than risk deleting the record for a file that's still
			// really there.
			result.Errors = append(result.Errors, fmt.Sprintf("check %s: %v", tf.Path, statErr))
			continue
		}
		if err := s.db.DeleteTrackFile(tf.ID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("prune missing %s: %v", tf.Path, err))
			continue
		}
		result.FilesRemoved++
	}
	if result.FilesRemoved > 0 {
		// This album may now have zero files left — see
		// ReapOrphanedAlbum's own doc comment for why that dead end
		// (invisible in Owned/Missing/Wanted alike) matters.
		if err := s.db.ReapOrphanedAlbum(albumID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("reap orphaned album %d: %v", albumID, err))
		}
	}

	groups = groupMultiDiscFolders(groups)

	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		s.matchFolder(ctx, groups[d], result)
	}
	return result, nil
}

// commonAncestorDir returns the deepest directory that is an ancestor of
// (or equal to) every directory in dirs — component-wise, not a string
// prefix (so "/music/Album" and "/music/Album2" correctly reduce to
// "/music" rather than the meaningless partial-string overlap "/music/Album").
// Used by ScanAlbumFolder to find one walk root that covers every existing
// disc subfolder of a multi-disc album. dirs is assumed non-empty.
func commonAncestorDir(dirs []string) string {
	common := strings.Split(filepath.Clean(dirs[0]), string(filepath.Separator))
	for _, d := range dirs[1:] {
		parts := strings.Split(filepath.Clean(d), string(filepath.Separator))
		n := len(common)
		if len(parts) < n {
			n = len(parts)
		}
		i := 0
		for i < n && common[i] == parts[i] {
			i++
		}
		common = common[:i]
	}
	if len(common) == 0 {
		return string(filepath.Separator)
	}
	joined := strings.Join(common, string(filepath.Separator))
	if joined == "" {
		return string(filepath.Separator)
	}
	return joined
}

// upsertFile reads path's tags, stats it, and upserts its track_files
// row — the part of matching that must run inline during the walk (every
// file needs a row before folder-grouping can even key on it). Matching
// itself happens afterward, in ScanRootFolder's post-walk per-folder pass.
func (s *Scanner) upsertFile(rf musiclibrary.RootFolder, path string, result *ScanResult) (*musiclibrary.TrackFile, *tagreader.Tags, error) {
	result.FilesFound++

	tags, err := tagreader.Read(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read tags: %w", err)
	}

	info, err := fileInfoOrZero(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}

	tf, err := s.db.UpsertTrackFileByPath(rf.ID, path, info.size, tags.Format, 0, 0, tagsJSON(tags))
	if err != nil {
		return nil, nil, fmt.Errorf("upsert track file: %w", err)
	}
	return tf, tags, nil
}

// recordFileResult folds one file's match attempt into result — an error
// is recorded (never fatal to the rest of the scan), a real match bumps
// FilesMatched and organizes on match if configured. Shared by
// matchFolder and its fallback paths (folder_match.go).
func (s *Scanner) recordFileResult(result *ScanResult, tf *musiclibrary.TrackFile, matched bool, err error) {
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", tf.Path, err))
		return
	}
	if !matched {
		return
	}
	result.FilesMatched++
	if s.getOrganizeOnMatch() {
		if _, oerr := s.OrganizeFile(tf.ID); oerr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: organize: %v", tf.Path, oerr))
			return
		}
		result.FilesOrganized++
	}
}

type statInfo struct{ size int64 }

func fileInfoOrZero(path string) (statInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return statInfo{}, err
	}
	return statInfo{size: st.Size()}, nil
}

// tagsJSON serializes a file's tags for storage in track_files.tags_json —
// kept around mainly so the manual-review UI can show what was actually
// read off a file without CantiNode needing to re-read it from disk.
// Marshal failure is not expected for this plain struct; falling back to
// "{}" rather than propagating the error keeps a scan from aborting over
// a display-only field.
func tagsJSON(t *tagreader.Tags) string {
	b, err := json.Marshal(t)
	if err != nil {
		return "{}"
	}
	return string(b)
}
