package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MatchStatus is a TrackFile's matching state.
type MatchStatus string

const (
	// StatusUnmatched means the scanner has not found a confident
	// MusicBrainz match yet — awaiting either a better scan pass or manual
	// review.
	StatusUnmatched MatchStatus = "unmatched"
	// StatusMatched means the scanner matched the file automatically,
	// either directly by an MBID already embedded in its tags or via a
	// fuzzy search that scored above the configured confidence threshold.
	StatusMatched MatchStatus = "matched"
	// StatusManual means a human linked the file to a track through the
	// review UI/API.
	StatusManual MatchStatus = "manual"
)

// TrackFile is a single audio file found on disk by a scan. TrackID is
// nil until the file is matched (see MatchStatus).
type TrackFile struct {
	ID              int64       `json:"id"`
	RootFolderID    int64       `json:"root_folder_id"`
	TrackID         *int64      `json:"track_id"`
	Path            string      `json:"path"`
	SizeBytes       int64       `json:"size_bytes"`
	Format          string      `json:"format"`
	BitrateKbps     int         `json:"bitrate_kbps"`
	DurationMs      int64       `json:"duration_ms"`
	TagsJSON        string      `json:"tags_json"`
	MatchStatus     MatchStatus `json:"match_status"`
	MatchConfidence float64     `json:"match_confidence"`
	ScannedAt       time.Time   `json:"scanned_at"`
	OrganizedAt     *time.Time  `json:"organized_at,omitempty"`
}

const trackFileSelect = `SELECT id, root_folder_id, track_id, path, size_bytes, format, bitrate_kbps, duration_ms, tags_json, match_status, match_confidence, scanned_at, organized_at FROM track_files`

func scanTrackFile(row interface{ Scan(...any) error }) (*TrackFile, error) {
	var tf TrackFile
	var trackID sql.NullInt64
	var organizedAt sql.NullTime
	if err := row.Scan(&tf.ID, &tf.RootFolderID, &trackID, &tf.Path, &tf.SizeBytes, &tf.Format,
		&tf.BitrateKbps, &tf.DurationMs, &tf.TagsJSON, &tf.MatchStatus, &tf.MatchConfidence,
		&tf.ScannedAt, &organizedAt); err != nil {
		return nil, err
	}
	if trackID.Valid {
		tf.TrackID = &trackID.Int64
	}
	if organizedAt.Valid {
		tf.OrganizedAt = &organizedAt.Time
	}
	return &tf, nil
}

// UpsertTrackFileByPath inserts a new track file, or — if path is already
// known — refreshes its raw file metadata (size/format/bitrate/duration/
// tags) without touching its existing match_status or track_id. A rescan
// re-reading a file's tags should never silently unlink a match a previous
// scan (or a human) already made; only SetTrackFileMatch does that.
func (db *DB) UpsertTrackFileByPath(ctx context.Context, rootFolderID int64, path string, sizeBytes int64, format string, bitrateKbps int, durationMs int64, tagsJSON string) (*TrackFile, error) {
	now := time.Now().UTC()

	existing, err := db.getTrackFileByPath(ctx, path)
	if err == nil {
		if _, err := db.ExecContext(ctx,
			`UPDATE track_files SET size_bytes = ?, format = ?, bitrate_kbps = ?, duration_ms = ?, tags_json = ?, scanned_at = ? WHERE id = ?`,
			sizeBytes, format, bitrateKbps, durationMs, tagsJSON, now, existing.ID); err != nil {
			return nil, fmt.Errorf("update track file: %w", err)
		}
		existing.SizeBytes, existing.Format, existing.BitrateKbps, existing.DurationMs, existing.TagsJSON, existing.ScannedAt =
			sizeBytes, format, bitrateKbps, durationMs, tagsJSON, now
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO track_files (root_folder_id, path, size_bytes, format, bitrate_kbps, duration_ms, tags_json, match_status, match_confidence, scanned_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		rootFolderID, path, sizeBytes, format, bitrateKbps, durationMs, tagsJSON, StatusUnmatched, now)
	if err != nil {
		return nil, fmt.Errorf("insert track file: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &TrackFile{
		ID: id, RootFolderID: rootFolderID, Path: path, SizeBytes: sizeBytes, Format: format,
		BitrateKbps: bitrateKbps, DurationMs: durationMs, TagsJSON: tagsJSON,
		MatchStatus: StatusUnmatched, ScannedAt: now,
	}, nil
}

func (db *DB) getTrackFileByPath(ctx context.Context, path string) (*TrackFile, error) {
	tf, err := scanTrackFile(db.QueryRowContext(ctx, trackFileSelect+` WHERE path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track file by path: %w", err)
	}
	return tf, nil
}

// GetTrackFile returns a single track file by ID, or ErrNotFound.
func (db *DB) GetTrackFile(ctx context.Context, id int64) (*TrackFile, error) {
	tf, err := scanTrackFile(db.QueryRowContext(ctx, trackFileSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track file: %w", err)
	}
	return tf, nil
}

// SetTrackFileMatch links id to trackID with the given status/confidence.
// trackID nil moves the file back to unmatched (used when a manual link is
// removed).
func (db *DB) SetTrackFileMatch(ctx context.Context, id int64, trackID *int64, status MatchStatus, confidence float64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE track_files SET track_id = ?, match_status = ?, match_confidence = ? WHERE id = ?`,
		trackID, status, confidence, id)
	if err != nil {
		return fmt.Errorf("set track file match: %w", err)
	}
	return nil
}

// SetTrackFileOrganized records that id was moved to newPath by the
// organizer.
func (db *DB) SetTrackFileOrganized(ctx context.Context, id int64, newPath string, organizedAt time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE track_files SET path = ?, organized_at = ? WHERE id = ?`, newPath, organizedAt, id)
	if err != nil {
		return fmt.Errorf("set track file organized: %w", err)
	}
	return nil
}

// ListTrackFilesByStatus returns every track file with the given match
// status, most recently scanned first.
func (db *DB) ListTrackFilesByStatus(ctx context.Context, status MatchStatus) ([]TrackFile, error) {
	rows, err := db.QueryContext(ctx, trackFileSelect+` WHERE match_status = ? ORDER BY scanned_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("list track files by status: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByTrack returns every file matched to trackID (normally
// zero or one, but duplicates on disk are possible).
func (db *DB) ListTrackFilesByTrack(ctx context.Context, trackID int64) ([]TrackFile, error) {
	rows, err := db.QueryContext(ctx, trackFileSelect+` WHERE track_id = ? ORDER BY path`, trackID)
	if err != nil {
		return nil, fmt.Errorf("list track files by track: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByRootFolder returns every file scanned from rootFolderID.
func (db *DB) ListTrackFilesByRootFolder(ctx context.Context, rootFolderID int64) ([]TrackFile, error) {
	rows, err := db.QueryContext(ctx, trackFileSelect+` WHERE root_folder_id = ? ORDER BY path`, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("list track files by root folder: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

func scanTrackFileRows(rows *sql.Rows) ([]TrackFile, error) {
	var out []TrackFile
	for rows.Next() {
		tf, err := scanTrackFile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan track file: %w", err)
		}
		out = append(out, *tf)
	}
	return out, rows.Err()
}

// DeleteTrackFilesMissing removes every track_files row under rootFolderID
// whose path is not in seenPaths — files a scan no longer finds on disk,
// because they were moved (outside CantiNode's own organizer, which
// updates the row instead) or deleted. Called once per root folder at the
// end of a scan pass, after every file actually found has already been
// upserted. Returns the number of rows removed.
func (db *DB) DeleteTrackFilesMissing(ctx context.Context, rootFolderID int64, seenPaths []string) (int64, error) {
	existing, err := db.ListTrackFilesByRootFolder(ctx, rootFolderID)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool, len(seenPaths))
	for _, p := range seenPaths {
		seen[p] = true
	}

	var removed int64
	for _, tf := range existing {
		if seen[tf.Path] {
			continue
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM track_files WHERE id = ?`, tf.ID); err != nil {
			return removed, fmt.Errorf("delete missing track file %d: %w", tf.ID, err)
		}
		removed++
	}
	return removed, nil
}
