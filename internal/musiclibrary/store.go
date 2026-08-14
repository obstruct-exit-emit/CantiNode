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
// table. Several can exist at once, each independently named; exactly one
// is IsDefault at any time (see SetDefaultRootFolder) — the fallback
// destination for a new automatic grab that has no artist-specific folder
// of its own to join yet (see internal/importer's targetRootFolder).
type RootFolder struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDefault bool   `json:"isDefault"`
	CreatedAt string `json:"createdAt"`
}

const rootFolderSelect = `SELECT id, name, path, is_default, created_at FROM root_folders`

func scanRootFolder(row interface{ Scan(...any) error }) (RootFolder, error) {
	var rf RootFolder
	err := row.Scan(&rf.ID, &rf.Name, &rf.Path, &rf.IsDefault, &rf.CreatedAt)
	return rf, err
}

// ListRootFolders returns every music root folder, ordered by name.
func (s *Store) ListRootFolders() ([]RootFolder, error) {
	rows, err := s.db.Query(rootFolderSelect + ` WHERE media_type = 'music' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	defer rows.Close()

	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see ListArtists' identical note.
	out := []RootFolder{}
	for rows.Next() {
		rf, err := scanRootFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan root folder: %w", err)
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// GetRootFolder returns a single music root folder by ID, or ErrNotFound.
func (s *Store) GetRootFolder(id int64) (*RootFolder, error) {
	rf, err := scanRootFolder(s.db.QueryRow(rootFolderSelect+` WHERE id = ? AND media_type = 'music'`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get root folder: %w", err)
	}
	return &rf, nil
}

// CreateRootFolder inserts a new music root folder — a low-level
// primitive with no path/existence validation of its own (the API layer
// already does that before calling this; see handleAddRootFolder). If no
// music root folder currently has one marked default, this one becomes
// default — insert and that check happen in the same transaction, so two
// concurrent adds to an empty table can't each see "no default yet" and
// both skip setting one (the gap a separate, non-transactional
// COUNT(*)-then-UPDATE would have).
func (s *Store) CreateRootFolder(path, name string) (*RootFolder, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO root_folders (media_type, path, name) VALUES ('music', ?, ?)`, path, name)
	if err != nil {
		return nil, fmt.Errorf("create root folder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create root folder: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE root_folders SET is_default = 1
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM root_folders WHERE media_type = 'music' AND is_default = 1)`,
		id); err != nil {
		return nil, fmt.Errorf("set default if none: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetRootFolder(id)
}

// DeleteRootFolder removes a music root folder — its track_files cascade-
// delete via the FK (ON DELETE CASCADE). If the deleted folder was the
// default, promotes another remaining music root folder (the lowest id)
// to default instead of silently leaving none marked — the invariant
// DefaultRootFolder's own doc comment promises ("SetDefaultRootFolder...
// keep exactly one marked whenever at least one exists"), which this used
// to be the one path that could break.
func (s *Store) DeleteRootFolder(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var wasDefault bool
	err = tx.QueryRow(`SELECT is_default FROM root_folders WHERE id = ? AND media_type = 'music'`, id).Scan(&wasDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check default before delete: %w", err)
	}

	res, err := tx.Exec(`DELETE FROM root_folders WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete root folder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if wasDefault {
		// A no-op if this was the last music root folder — MIN(id) is
		// NULL, matching nothing.
		if _, err := tx.Exec(`
			UPDATE root_folders SET is_default = 1
			WHERE media_type = 'music' AND id = (SELECT MIN(id) FROM root_folders WHERE media_type = 'music')`); err != nil {
			return fmt.Errorf("promote new default after delete: %w", err)
		}
	}

	return tx.Commit()
}

// DefaultRootFolder returns the one music root folder currently marked
// IsDefault, or ErrNotFound if none is (only possible with zero root
// folders configured at all — SetDefaultRootFolder and the initial
// migration backfill both keep exactly one marked whenever at least one
// exists).
func (s *Store) DefaultRootFolder() (*RootFolder, error) {
	rf, err := scanRootFolder(s.db.QueryRow(rootFolderSelect + ` WHERE media_type = 'music' AND is_default = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get default root folder: %w", err)
	}
	return &rf, nil
}

// ArtistRootFolder returns the root folder holding the most of artistID's
// existing MATCHED track files (ties broken toward the lowest
// root_folder_id, for determinism) — used to send a new automatic grab to
// join an artist's existing discography instead of wherever the instance-
// wide default happens to be. ErrNotFound means the artist owns no
// matched files anywhere yet — including when every file scanned in under
// their name so far is still unmatched, since an unmatched file isn't
// linked to any artist at all (that link is exactly what "matched" means)
// and so can't be found by an artist-scoped query no matter how it's
// written. A rare, narrow edge case in practice: it only changes anything
// for an artist whose files are ALL still unmatched.
func (s *Store) ArtistRootFolder(artistID int64) (*RootFolder, error) {
	var rootFolderID int64
	err := s.db.QueryRow(`
		SELECT tf.root_folder_id
		FROM track_files tf
		JOIN tracks t ON t.id = tf.track_id
		JOIN albums al ON al.id = t.album_id
		WHERE al.artist_id = ?
		GROUP BY tf.root_folder_id
		ORDER BY COUNT(*) DESC, tf.root_folder_id ASC
		LIMIT 1`, artistID).Scan(&rootFolderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find artist root folder: %w", err)
	}
	return s.GetRootFolder(rootFolderID)
}

// RenameRootFolder sets a music root folder's display name — purely
// cosmetic, never touches path or any on-disk file.
func (s *Store) RenameRootFolder(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE root_folders SET name = ? WHERE id = ? AND media_type = 'music'`, name, id)
	if err != nil {
		return fmt.Errorf("rename root folder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDefaultRootFolder marks id as the one default music root folder,
// clearing the flag from whichever one (if any) previously held it —
// always exactly zero or one row has is_default = 1, never more.
func (s *Store) SetDefaultRootFolder(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE root_folders SET is_default = 0 WHERE media_type = 'music'`); err != nil {
		return fmt.Errorf("clear existing default: %w", err)
	}
	res, err := tx.Exec(`UPDATE root_folders SET is_default = 1 WHERE id = ? AND media_type = 'music'`, id)
	if err != nil {
		return fmt.Errorf("set default: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
