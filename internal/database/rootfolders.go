package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by a Get-style lookup when no row matches.
var ErrNotFound = errors.New("not found")

// RootFolder is a filesystem path the scanner walks for audio files.
type RootFolder struct {
	ID        int64     `json:"id"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateRootFolder inserts a new root folder. path must be unique.
func (db *DB) CreateRootFolder(ctx context.Context, path string) (*RootFolder, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO root_folders (path, created_at) VALUES (?, ?)`, path, now)
	if err != nil {
		return nil, fmt.Errorf("insert root folder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &RootFolder{ID: id, Path: path, CreatedAt: now}, nil
}

// ListRootFolders returns every root folder, ordered by path.
func (db *DB) ListRootFolders(ctx context.Context) ([]RootFolder, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, path, created_at FROM root_folders ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	defer rows.Close()

	var out []RootFolder
	for rows.Next() {
		var rf RootFolder
		if err := rows.Scan(&rf.ID, &rf.Path, &rf.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan root folder: %w", err)
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// GetRootFolder returns a single root folder by ID, or ErrNotFound.
func (db *DB) GetRootFolder(ctx context.Context, id int64) (*RootFolder, error) {
	var rf RootFolder
	err := db.QueryRowContext(ctx, `SELECT id, path, created_at FROM root_folders WHERE id = ?`, id).
		Scan(&rf.ID, &rf.Path, &rf.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get root folder: %w", err)
	}
	return &rf, nil
}

// DeleteRootFolder removes a root folder. Its track_files cascade-delete
// (see migrations/0001_init.sql) — the matched artists/albums/tracks they
// pointed at are left in place, since another root folder's files may
// still reference the same library entities.
func (db *DB) DeleteRootFolder(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM root_folders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete root folder: %w", err)
	}
	return nil
}
