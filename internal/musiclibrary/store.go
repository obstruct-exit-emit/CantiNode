// Package musiclibrary holds CantiNode's music domain model — artists,
// albums, tracks, and the track files matched against them — and the
// SQLite persistence layer for it, ported from CantiNode's own original,
// from-scratch schema (before this codebase was rebuilt on top of a fork
// of LibriNode; see migrations/018_music.sql). Kept as its
// own package rather than folded into internal/library: track-level
// matching (disc/track position, per-file MusicBrainz recording IDs,
// embedded-tag confidence) doesn't fit the prose book/edition shape, and
// an artist's identity is its MusicBrainz MBID, not a books-style
// (source, foreign_id) pair.
package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned by a Get-style lookup when no row matches.
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// RootFolder is a filesystem path the scanner walks for audio files — the
// media_type='music' slice of the same root_folders table every other
// library uses (internal/library.RootFolder), not a second music-specific
// table.
type RootFolder struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	CreatedAt string `json:"createdAt"`
}

// ListRootFolders returns every music root folder, ordered by path.
func (s *Store) ListRootFolders() ([]RootFolder, error) {
	rows, err := s.db.Query(`SELECT id, path, created_at FROM root_folders WHERE media_type = 'music' ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	defer rows.Close()

	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see ListArtists' identical note.
	out := []RootFolder{}
	for rows.Next() {
		var rf RootFolder
		if err := rows.Scan(&rf.ID, &rf.Path, &rf.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan root folder: %w", err)
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// GetRootFolder returns a single music root folder by ID, or ErrNotFound.
func (s *Store) GetRootFolder(id int64) (*RootFolder, error) {
	var rf RootFolder
	err := s.db.QueryRow(`SELECT id, path, created_at FROM root_folders WHERE id = ? AND media_type = 'music'`, id).
		Scan(&rf.ID, &rf.Path, &rf.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get root folder: %w", err)
	}
	return &rf, nil
}
