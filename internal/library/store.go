// Package library holds the generic tables shared by every media type —
// root_folders (listed here; created/deleted directly in internal/api) and
// quality_profiles (profiles.go). It used to also own the ebook/comic
// author/book/series/edition domain; that's gone now that ebook/comic
// support has been removed, leaving this package as just the shared
// root-folder/quality-profile infrastructure music depends on too.
package library

import (
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}
