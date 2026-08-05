// Package scanner walks CantiNode's root folders, reads each audio file's
// tags (internal/tagreader), matches it against MusicBrainz
// (internal/musicbrainz), and organizes matched files on disk into a
// consistent layout.
package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/tagreader"
)

// Scanner ties the database, MusicBrainz client, and tag reader together
// into the scan -> match -> organize pipeline.
type Scanner struct {
	db     *database.DB
	mb     *musicbrainz.Client
	logger *slog.Logger

	// settingsMu guards namingFormat/minMatchConfidence/organizeOnMatch —
	// internal/api's settings endpoint calls UpdateSettings from an HTTP
	// handler goroutine while a scan (reading them from ScanRootFolder's
	// own goroutine) may be in flight, so a plain unsynchronized field
	// would be a real data race, not just a style nitpick.
	settingsMu         sync.RWMutex
	namingFormat       string
	minMatchConfidence float64
	organizeOnMatch    bool
}

// New returns a Scanner. namingFormat/minMatchConfidence/organizeOnMatch
// mirror the equivalent config.Config fields (kept as plain parameters
// here rather than a *config.Config dependency, so this package doesn't
// need to import internal/config just to read three values) — see
// UpdateSettings to change them after construction.
func New(db *database.DB, mb *musicbrainz.Client, logger *slog.Logger, namingFormat string, minMatchConfidence float64, organizeOnMatch bool) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		db:                 db,
		mb:                 mb,
		logger:             logger,
		namingFormat:       namingFormat,
		minMatchConfidence: minMatchConfidence,
		organizeOnMatch:    organizeOnMatch,
	}
}

// UpdateSettings applies a live settings change (from internal/api's
// PUT /api/v1/settings) to this Scanner — takes effect on the very next
// file scanned/organized, no restart needed.
func (s *Scanner) UpdateSettings(namingFormat string, minMatchConfidence float64, organizeOnMatch bool) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.namingFormat = namingFormat
	s.minMatchConfidence = minMatchConfidence
	s.organizeOnMatch = organizeOnMatch
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

// ScanResult summarizes one scan pass (either a single root folder or
// every root folder), for the API/UI to report on.
type ScanResult struct {
	FilesFound     int
	FilesMatched   int
	FilesOrganized int
	FilesRemoved   int // rows deleted because the file is no longer on disk
	Errors         []string
}

func (r *ScanResult) merge(other ScanResult) {
	r.FilesFound += other.FilesFound
	r.FilesMatched += other.FilesMatched
	r.FilesOrganized += other.FilesOrganized
	r.FilesRemoved += other.FilesRemoved
	r.Errors = append(r.Errors, other.Errors...)
}

// ScanAll scans every configured root folder in turn.
func (s *Scanner) ScanAll(ctx context.Context) (*ScanResult, error) {
	folders, err := s.db.ListRootFolders(ctx)
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
			return result, fmt.Errorf("scan root folder %s: %w", rf.Path, err)
		}
		result.merge(*r)
	}
	return result, nil
}

// ScanRootFolder walks rf.Path for audio files, upserts a track_files row
// per file found, attempts to match every not-yet-matched file, and
// removes rows for files no longer present on disk. A per-file read or
// match error is recorded in the result and does not stop the scan.
func (s *Scanner) ScanRootFolder(ctx context.Context, rf database.RootFolder) (*ScanResult, error) {
	// Errors initialized non-nil so it JSON-encodes to [] rather than
	// null when empty — internal/api returns a ScanResult straight
	// through to the frontend.
	result := &ScanResult{Errors: []string{}}
	var seenPaths []string

	err := filepath.WalkDir(rf.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("walk %s: %v", path, err))
			return nil
		}
		if d.IsDir() || !tagreader.IsAudioFile(path) {
			return nil
		}
		seenPaths = append(seenPaths, path)

		if scanErr := s.scanFile(ctx, rf, path, result); scanErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, scanErr))
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("walk %s: %w", rf.Path, err)
	}

	removed, err := s.db.DeleteTrackFilesMissing(ctx, rf.ID, seenPaths)
	if err != nil {
		return result, fmt.Errorf("prune missing files: %w", err)
	}
	result.FilesRemoved = int(removed)

	return result, nil
}

func (s *Scanner) scanFile(ctx context.Context, rf database.RootFolder, path string, result *ScanResult) error {
	result.FilesFound++

	tags, err := tagreader.Read(path)
	if err != nil {
		return fmt.Errorf("read tags: %w", err)
	}

	info, err := fileInfoOrZero(path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	tf, err := s.db.UpsertTrackFileByPath(ctx, rf.ID, path, info.size, tags.Format, 0, 0, tagsJSON(tags))
	if err != nil {
		return fmt.Errorf("upsert track file: %w", err)
	}

	if tf.MatchStatus != database.StatusUnmatched {
		// Already matched (auto or manual) by a previous scan — never
		// silently re-decided here. A user who wants to redo a match uses
		// the manual-match API explicitly.
		return nil
	}

	matched, err := s.matchFile(ctx, tf, tags)
	if err != nil {
		return fmt.Errorf("match: %w", err)
	}
	if matched {
		result.FilesMatched++
		if s.getOrganizeOnMatch() {
			if _, err := s.OrganizeFile(ctx, tf.ID); err != nil {
				return fmt.Errorf("organize: %w", err)
			}
			result.FilesOrganized++
		}
	}
	return nil
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
