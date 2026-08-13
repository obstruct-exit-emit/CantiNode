package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	RootFolderID    int64       `json:"rootFolderId"`
	TrackID         *int64      `json:"trackId"`
	Path            string      `json:"path"`
	SizeBytes       int64       `json:"sizeBytes"`
	Format          string      `json:"format"`
	BitrateKbps     int         `json:"bitrateKbps"`
	DurationMs      int64       `json:"durationMs"`
	TagsJSON        string      `json:"tagsJson"`
	MatchStatus     MatchStatus `json:"matchStatus"`
	MatchConfidence float64     `json:"matchConfidence"`
	ScannedAt       time.Time   `json:"scannedAt"`
	OrganizedAt     *time.Time  `json:"organizedAt,omitempty"`
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
func (s *Store) UpsertTrackFileByPath(rootFolderID int64, path string, sizeBytes int64, format string, bitrateKbps int, durationMs int64, tagsJSON string) (*TrackFile, error) {
	now := time.Now().UTC()

	existing, err := s.getTrackFileByPath(path)
	if err == nil {
		if _, err := s.db.Exec(
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

	res, err := s.db.Exec(
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

func (s *Store) getTrackFileByPath(path string) (*TrackFile, error) {
	tf, err := scanTrackFile(s.db.QueryRow(trackFileSelect+` WHERE path = ?`, path))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track file by path: %w", err)
	}
	return tf, nil
}

// GetTrackFile returns a single track file by ID, or ErrNotFound.
func (s *Store) GetTrackFile(id int64) (*TrackFile, error) {
	tf, err := scanTrackFile(s.db.QueryRow(trackFileSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track file: %w", err)
	}
	return tf, nil
}

// ListTrackFilesByIDs returns every track file in ids, keyed by ID — the
// bulk form of GetTrackFile, used by musicscanner's SuggestMatches to avoid
// one round trip per file when proposing matches for a whole folder's worth
// of files at once. An id with no matching row (already deleted, etc.) is
// simply absent from the result rather than erroring the whole batch.
func (s *Store) ListTrackFilesByIDs(ids []int64) (map[int64]*TrackFile, error) {
	out := make(map[int64]*TrackFile, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(trackFileSelect+` WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("list track files by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		tf, err := scanTrackFile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan track file: %w", err)
		}
		out[tf.ID] = tf
	}
	return out, rows.Err()
}

// SetTrackFileMatch links id to trackID with the given status/confidence.
// trackID nil moves the file back to unmatched (used when a manual link is
// removed).
func (s *Store) SetTrackFileMatch(id int64, trackID *int64, status MatchStatus, confidence float64) error {
	_, err := s.db.Exec(
		`UPDATE track_files SET track_id = ?, match_status = ?, match_confidence = ? WHERE id = ?`,
		trackID, status, confidence, id)
	if err != nil {
		return fmt.Errorf("set track file match: %w", err)
	}
	return nil
}

// SetTrackFileOrganized records that id was moved to newPath by the
// organizer.
func (s *Store) SetTrackFileOrganized(id int64, newPath string, organizedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE track_files SET path = ?, organized_at = ? WHERE id = ?`, newPath, organizedAt, id)
	if err != nil {
		return fmt.Errorf("set track file organized: %w", err)
	}
	return nil
}

// ListTrackFilesByStatus returns every track file with the given match
// status, most recently scanned first.
func (s *Store) ListTrackFilesByStatus(status MatchStatus) ([]TrackFile, error) {
	rows, err := s.db.Query(trackFileSelect+` WHERE match_status = ? ORDER BY scanned_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("list track files by status: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByTrack returns every file matched to trackID (normally
// zero or one, but duplicates on disk are possible).
func (s *Store) ListTrackFilesByTrack(trackID int64) ([]TrackFile, error) {
	rows, err := s.db.Query(trackFileSelect+` WHERE track_id = ? ORDER BY path`, trackID)
	if err != nil {
		return nil, fmt.Errorf("list track files by track: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByArtist returns every track file under any album/track
// belonging to artistID (joined track_files -> tracks -> albums), ordered
// by path. Backs the artist page's Organize preview/apply and
// RemoveArtist, both of which need every one of an artist's own files
// regardless of match status (organize skips unmatched ones itself;
// RemoveArtist needs to touch every one of them, matched or not).
func (s *Store) ListTrackFilesByArtist(artistID int64) ([]TrackFile, error) {
	rows, err := s.db.Query(`
		SELECT tf.id, tf.root_folder_id, tf.track_id, tf.path, tf.size_bytes, tf.format, tf.bitrate_kbps, tf.duration_ms, tf.tags_json, tf.match_status, tf.match_confidence, tf.scanned_at, tf.organized_at
		FROM track_files tf
		JOIN tracks t ON t.id = tf.track_id
		JOIN albums al ON al.id = t.album_id
		WHERE al.artist_id = ?
		ORDER BY tf.path`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list track files by artist: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByAlbum returns every track file under any track belonging
// to albumID (joined track_files -> tracks), ordered by path. Backs the
// album page's Scan/Organize/Remove actions, which — unlike the artist-wide
// versions — must never touch a sibling album's files.
func (s *Store) ListTrackFilesByAlbum(albumID int64) ([]TrackFile, error) {
	rows, err := s.db.Query(`
		SELECT tf.id, tf.root_folder_id, tf.track_id, tf.path, tf.size_bytes, tf.format, tf.bitrate_kbps, tf.duration_ms, tf.tags_json, tf.match_status, tf.match_confidence, tf.scanned_at, tf.organized_at
		FROM track_files tf
		JOIN tracks t ON t.id = tf.track_id
		WHERE t.album_id = ?
		ORDER BY tf.path`, albumID)
	if err != nil {
		return nil, fmt.Errorf("list track files by album: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

// ListTrackFilesByRootFolder returns every file scanned from rootFolderID.
func (s *Store) ListTrackFilesByRootFolder(rootFolderID int64) ([]TrackFile, error) {
	rows, err := s.db.Query(trackFileSelect+` WHERE root_folder_id = ? ORDER BY path`, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("list track files by root folder: %w", err)
	}
	defer rows.Close()
	return scanTrackFileRows(rows)
}

func scanTrackFileRows(rows *sql.Rows) ([]TrackFile, error) {
	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see Store.ListArtists' identical note.
	out := []TrackFile{}
	for rows.Next() {
		tf, err := scanTrackFile(rows)
		if err != nil {
			return nil, fmt.Errorf("scan track file: %w", err)
		}
		out = append(out, *tf)
	}
	return out, rows.Err()
}

// DeleteTrackFile removes a single track_files row by ID — used when the
// user explicitly deletes a file (see Scanner.DeleteTrackFile, which
// removes the file on disk first). Distinct from DeleteTrackFilesMissing,
// which reconciles a whole root folder against what a scan actually
// found rather than removing one row on request.
func (s *Store) DeleteTrackFile(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM track_files WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete track file %d: %w", id, err)
	}
	return nil
}

// DeleteTrackFilesMissing removes every track_files row under rootFolderID
// whose path is not in seenPaths — files a scan no longer finds on disk,
// because they were moved (outside the organizer, which updates the row
// instead) or deleted. Called once per root folder at the end of a scan
// pass, after every file actually found has already been upserted.
// Returns the number of rows removed.
func (s *Store) DeleteTrackFilesMissing(rootFolderID int64, seenPaths []string) (int64, error) {
	existing, err := s.ListTrackFilesByRootFolder(rootFolderID)
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
		if _, err := s.db.Exec(`DELETE FROM track_files WHERE id = ?`, tf.ID); err != nil {
			return removed, fmt.Errorf("delete missing track file %d: %w", tf.ID, err)
		}
		removed++
	}
	return removed, nil
}
