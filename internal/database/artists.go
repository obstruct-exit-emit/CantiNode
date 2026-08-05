package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Artist is a library artist, matched to a MusicBrainz artist MBID.
type Artist struct {
	ID        int64     `json:"id"`
	MBID      string    `json:"mbid"`
	Name      string    `json:"name"`
	SortName  string    `json:"sort_name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetOrCreateArtist returns the existing artist for mbid, inserting one if
// none exists yet. Artists are only ever created as a side effect of
// matching a track file — there's no standalone "add an artist" flow in
// v1 — so mbid is always known by the time this is called.
func (db *DB) GetOrCreateArtist(ctx context.Context, mbid, name, sortName string) (*Artist, error) {
	existing, err := db.getArtistByMBID(ctx, mbid)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO artists (mbid, name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		mbid, name, sortName, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert artist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Artist{ID: id, MBID: mbid, Name: name, SortName: sortName, CreatedAt: now, UpdatedAt: now}, nil
}

func (db *DB) getArtistByMBID(ctx context.Context, mbid string) (*Artist, error) {
	var a Artist
	err := db.QueryRowContext(ctx,
		`SELECT id, mbid, name, sort_name, created_at, updated_at FROM artists WHERE mbid = ?`, mbid).
		Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artist by mbid: %w", err)
	}
	return &a, nil
}

// GetArtist returns a single artist by ID, or ErrNotFound.
func (db *DB) GetArtist(ctx context.Context, id int64) (*Artist, error) {
	var a Artist
	err := db.QueryRowContext(ctx,
		`SELECT id, mbid, name, sort_name, created_at, updated_at FROM artists WHERE id = ?`, id).
		Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artist: %w", err)
	}
	return &a, nil
}

// ListArtists returns every artist with at least one track file, ordered
// by sort name, so the Library UI's top level only ever shows artists
// that actually have something in them.
func (db *DB) ListArtists(ctx context.Context) ([]Artist, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.mbid, a.name, a.sort_name, a.created_at, a.updated_at
		FROM artists a
		JOIN albums al ON al.artist_id = a.id
		JOIN tracks t ON t.album_id = al.id
		JOIN track_files tf ON tf.track_id = t.id
		ORDER BY a.sort_name`)
	if err != nil {
		return nil, fmt.Errorf("list artists: %w", err)
	}
	defer rows.Close()

	var out []Artist
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan artist: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
