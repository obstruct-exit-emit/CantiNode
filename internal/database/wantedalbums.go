package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WantedStatus is a WantedAlbum's acquisition state.
type WantedStatus string

const (
	WantedStatusWanted      WantedStatus = "wanted"
	WantedStatusDownloading WantedStatus = "downloading"
	WantedStatusDownloaded  WantedStatus = "downloaded"
	WantedStatusIgnored     WantedStatus = "ignored"
)

// WantedAlbum is one release group CantiNode is trying to acquire for a
// MonitoredArtist — see migrations/0002_acquisition.sql.
type WantedAlbum struct {
	ID                int64        `json:"id"`
	MonitoredArtistID int64        `json:"monitored_artist_id"`
	ReleaseGroupMBID  string       `json:"release_group_mbid"`
	Title             string       `json:"title"`
	PrimaryType       string       `json:"primary_type"`
	ReleaseDate       string       `json:"release_date"`
	Status            WantedStatus `json:"status"`
	AddedAt           time.Time    `json:"added_at"`
}

const wantedAlbumSelect = `SELECT id, monitored_artist_id, release_group_mbid, title, primary_type, release_date, status, added_at FROM wanted_albums`

func scanWantedAlbum(row interface{ Scan(...any) error }) (*WantedAlbum, error) {
	var w WantedAlbum
	if err := row.Scan(&w.ID, &w.MonitoredArtistID, &w.ReleaseGroupMBID, &w.Title, &w.PrimaryType, &w.ReleaseDate, &w.Status, &w.AddedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

// GetOrCreateWantedAlbum returns the existing wanted album for
// (monitoredArtistID, releaseGroupMBID), inserting one (as
// WantedStatusWanted) if none exists yet — the sync step (internal/
// acquisition) calls this once per release group a monitored artist has
// on MusicBrainz, so it's naturally idempotent across repeated syncs.
func (db *DB) GetOrCreateWantedAlbum(ctx context.Context, monitoredArtistID int64, releaseGroupMBID, title, primaryType, releaseDate string) (*WantedAlbum, error) {
	existing, err := db.getWantedAlbumByReleaseGroup(ctx, monitoredArtistID, releaseGroupMBID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO wanted_albums (monitored_artist_id, release_group_mbid, title, primary_type, release_date, status, added_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		monitoredArtistID, releaseGroupMBID, title, primaryType, releaseDate, WantedStatusWanted, now)
	if err != nil {
		return nil, fmt.Errorf("insert wanted album: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &WantedAlbum{
		ID: id, MonitoredArtistID: monitoredArtistID, ReleaseGroupMBID: releaseGroupMBID,
		Title: title, PrimaryType: primaryType, ReleaseDate: releaseDate,
		Status: WantedStatusWanted, AddedAt: now,
	}, nil
}

func (db *DB) getWantedAlbumByReleaseGroup(ctx context.Context, monitoredArtistID int64, releaseGroupMBID string) (*WantedAlbum, error) {
	w, err := scanWantedAlbum(db.QueryRowContext(ctx,
		wantedAlbumSelect+` WHERE monitored_artist_id = ? AND release_group_mbid = ?`, monitoredArtistID, releaseGroupMBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wanted album by release group: %w", err)
	}
	return w, nil
}

// GetWantedAlbum returns a single wanted album by ID, or ErrNotFound.
func (db *DB) GetWantedAlbum(ctx context.Context, id int64) (*WantedAlbum, error) {
	w, err := scanWantedAlbum(db.QueryRowContext(ctx, wantedAlbumSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wanted album: %w", err)
	}
	return w, nil
}

// ListWantedAlbumsByArtist returns every wanted album under
// monitoredArtistID, newest release first.
func (db *DB) ListWantedAlbumsByArtist(ctx context.Context, monitoredArtistID int64) ([]WantedAlbum, error) {
	rows, err := db.QueryContext(ctx, wantedAlbumSelect+` WHERE monitored_artist_id = ? ORDER BY release_date DESC, title`, monitoredArtistID)
	if err != nil {
		return nil, fmt.Errorf("list wanted albums by artist: %w", err)
	}
	defer rows.Close()
	return scanWantedAlbumRows(rows)
}

// ListWantedAlbumsByStatus returns every wanted album currently in
// status, across every monitored artist — backs the Wanted list's own
// view (status=wanted) and the acquisition search loop.
func (db *DB) ListWantedAlbumsByStatus(ctx context.Context, status WantedStatus) ([]WantedAlbum, error) {
	rows, err := db.QueryContext(ctx, wantedAlbumSelect+` WHERE status = ? ORDER BY added_at`, status)
	if err != nil {
		return nil, fmt.Errorf("list wanted albums by status: %w", err)
	}
	defer rows.Close()
	return scanWantedAlbumRows(rows)
}

func scanWantedAlbumRows(rows *sql.Rows) ([]WantedAlbum, error) {
	out := []WantedAlbum{}
	for rows.Next() {
		w, err := scanWantedAlbum(rows)
		if err != nil {
			return nil, fmt.Errorf("scan wanted album: %w", err)
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// SetWantedAlbumStatus updates id's acquisition status — e.g. 'wanted'
// -> 'downloading' the moment a release is grabbed, 'downloading' ->
// 'downloaded' once its download is imported.
func (db *DB) SetWantedAlbumStatus(ctx context.Context, id int64, status WantedStatus) error {
	_, err := db.ExecContext(ctx, `UPDATE wanted_albums SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set wanted album status: %w", err)
	}
	return nil
}
