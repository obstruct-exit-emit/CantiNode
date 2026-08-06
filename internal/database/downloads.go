package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DownloadProtocol is which download client (qBittorrent or SABnzbd) a
// Download was added through — see migrations/0002_acquisition.sql.
type DownloadProtocol string

const (
	ProtocolTorrent DownloadProtocol = "torrent"
	ProtocolUsenet  DownloadProtocol = "usenet"
)

// DownloadStatus is a Download's progress toward becoming library files.
type DownloadStatus string

const (
	DownloadStatusDownloading DownloadStatus = "downloading"
	DownloadStatusCompleted   DownloadStatus = "completed"
	DownloadStatusImported    DownloadStatus = "imported"
	DownloadStatusError       DownloadStatus = "error"
)

// Download is one grabbed release, tracked from the moment it's sent to
// its download client until its files are imported — see
// internal/acquisition and migrations/0002_acquisition.sql.
type Download struct {
	ID            int64            `json:"id"`
	WantedAlbumID int64            `json:"wanted_album_id"`
	RootFolderID  int64            `json:"root_folder_id"`
	Protocol      DownloadProtocol `json:"protocol"`
	ClientID      string           `json:"client_id"`
	Title         string           `json:"title"`
	Indexer       string           `json:"indexer"`
	Status        DownloadStatus   `json:"status"`
	ErrorMessage  string           `json:"error_message"`
	GrabbedAt     time.Time        `json:"grabbed_at"`
	CompletedAt   *time.Time       `json:"completed_at,omitempty"`
	ImportedAt    *time.Time       `json:"imported_at,omitempty"`
}

const downloadSelect = `SELECT id, wanted_album_id, root_folder_id, protocol, client_id, title, indexer, status, error_message, grabbed_at, completed_at, imported_at FROM downloads`

func scanDownload(row interface{ Scan(...any) error }) (*Download, error) {
	var d Download
	var completedAt, importedAt sql.NullTime
	if err := row.Scan(&d.ID, &d.WantedAlbumID, &d.RootFolderID, &d.Protocol, &d.ClientID, &d.Title, &d.Indexer,
		&d.Status, &d.ErrorMessage, &d.GrabbedAt, &completedAt, &importedAt); err != nil {
		return nil, err
	}
	if completedAt.Valid {
		d.CompletedAt = &completedAt.Time
	}
	if importedAt.Valid {
		d.ImportedAt = &importedAt.Time
	}
	return &d, nil
}

// CreateDownload records a freshly grabbed release.
func (db *DB) CreateDownload(ctx context.Context, wantedAlbumID, rootFolderID int64, protocol DownloadProtocol, clientID, title, indexer string) (*Download, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO downloads (wanted_album_id, root_folder_id, protocol, client_id, title, indexer, status, error_message, grabbed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`,
		wantedAlbumID, rootFolderID, protocol, clientID, title, indexer, DownloadStatusDownloading, now)
	if err != nil {
		return nil, fmt.Errorf("insert download: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Download{
		ID: id, WantedAlbumID: wantedAlbumID, RootFolderID: rootFolderID, Protocol: protocol,
		ClientID: clientID, Title: title, Indexer: indexer, Status: DownloadStatusDownloading, GrabbedAt: now,
	}, nil
}

// GetDownload returns a single download by ID, or ErrNotFound.
func (db *DB) GetDownload(ctx context.Context, id int64) (*Download, error) {
	d, err := scanDownload(db.QueryRowContext(ctx, downloadSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get download: %w", err)
	}
	return d, nil
}

// ListDownloadsByStatus returns every download currently in status —
// the acquisition poller's own worklist (DownloadStatusDownloading) and
// the Wanted UI's history view.
func (db *DB) ListDownloadsByStatus(ctx context.Context, status DownloadStatus) ([]Download, error) {
	rows, err := db.QueryContext(ctx, downloadSelect+` WHERE status = ? ORDER BY grabbed_at`, status)
	if err != nil {
		return nil, fmt.Errorf("list downloads by status: %w", err)
	}
	defer rows.Close()

	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, fmt.Errorf("scan download: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// ListDownloads returns every download, most recently grabbed first —
// backs the Wanted page's activity view.
func (db *DB) ListDownloads(ctx context.Context) ([]Download, error) {
	rows, err := db.QueryContext(ctx, downloadSelect+` ORDER BY grabbed_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list downloads: %w", err)
	}
	defer rows.Close()

	out := []Download{}
	for rows.Next() {
		d, err := scanDownload(rows)
		if err != nil {
			return nil, fmt.Errorf("scan download: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDownload removes a tracked download's own row — used when
// canceling a grab (see acquisition.Service.CancelDownload). Does not
// touch the download client itself or the wanted album's status; callers
// handle both of those first.
func (db *DB) DeleteDownload(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM downloads WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete download: %w", err)
	}
	return nil
}

// SetDownloadCompleted marks id as completed (the download client has
// the files on its own local disk, not yet imported into the library).
func (db *DB) SetDownloadCompleted(ctx context.Context, id int64, completedAt time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE downloads SET status = ?, completed_at = ? WHERE id = ?`, DownloadStatusCompleted, completedAt, id)
	if err != nil {
		return fmt.Errorf("set download completed: %w", err)
	}
	return nil
}

// SetDownloadImported marks id as imported — its files have been copied
// into root_folder_id and handed to internal/scanner.
func (db *DB) SetDownloadImported(ctx context.Context, id int64, importedAt time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE downloads SET status = ?, imported_at = ? WHERE id = ?`, DownloadStatusImported, importedAt, id)
	if err != nil {
		return fmt.Errorf("set download imported: %w", err)
	}
	return nil
}

// SetDownloadError marks id as failed with message — from either the
// download client itself reporting an error, or a problem on CantiNode's
// own side (e.g. the import copy failing).
func (db *DB) SetDownloadError(ctx context.Context, id int64, message string) error {
	_, err := db.ExecContext(ctx, `UPDATE downloads SET status = ?, error_message = ? WHERE id = ?`, DownloadStatusError, message, id)
	if err != nil {
		return fmt.Errorf("set download error: %w", err)
	}
	return nil
}
