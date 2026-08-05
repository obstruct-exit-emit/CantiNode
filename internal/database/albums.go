package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Album is a library album (MusicBrainz release), matched to a specific
// release MBID under ArtistID.
type Album struct {
	ID               int64     `json:"id"`
	ArtistID         int64     `json:"artist_id"`
	MBID             string    `json:"mbid"`
	ReleaseGroupMBID string    `json:"release_group_mbid"`
	Title            string    `json:"title"`
	ReleaseDate      string    `json:"release_date"`
	PrimaryType      string    `json:"primary_type"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GetOrCreateAlbum returns the existing album for mbid, inserting one if
// none exists yet.
func (db *DB) GetOrCreateAlbum(ctx context.Context, artistID int64, mbid, releaseGroupMBID, title, releaseDate, primaryType string) (*Album, error) {
	existing, err := db.getAlbumByMBID(ctx, mbid)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO albums (artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		artistID, mbid, releaseGroupMBID, title, releaseDate, primaryType, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert album: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Album{
		ID: id, ArtistID: artistID, MBID: mbid, ReleaseGroupMBID: releaseGroupMBID,
		Title: title, ReleaseDate: releaseDate, PrimaryType: primaryType,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (db *DB) getAlbumByMBID(ctx context.Context, mbid string) (*Album, error) {
	var a Album
	err := db.QueryRowContext(ctx, albumSelect+` WHERE mbid = ?`, mbid).
		Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album by mbid: %w", err)
	}
	return &a, nil
}

const albumSelect = `SELECT id, artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at FROM albums`

// GetAlbum returns a single album by ID, or ErrNotFound.
func (db *DB) GetAlbum(ctx context.Context, id int64) (*Album, error) {
	var a Album
	err := db.QueryRowContext(ctx, albumSelect+` WHERE id = ?`, id).
		Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album: %w", err)
	}
	return &a, nil
}

// ListAlbumsByArtist returns every album with at least one track file,
// under artistID, newest release first.
func (db *DB) ListAlbumsByArtist(ctx context.Context, artistID int64) ([]Album, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT al.id, al.artist_id, al.mbid, al.release_group_mbid, al.title, al.release_date, al.primary_type, al.created_at, al.updated_at
		FROM albums al
		JOIN tracks t ON t.album_id = al.id
		JOIN track_files tf ON tf.track_id = t.id
		WHERE al.artist_id = ?
		ORDER BY al.release_date DESC, al.title`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list albums by artist: %w", err)
	}
	defer rows.Close()

	var out []Album
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
