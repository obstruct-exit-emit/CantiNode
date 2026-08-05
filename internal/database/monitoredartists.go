package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MonitoredArtist is an artist the user has asked CantiNode to watch —
// see migrations/0002_acquisition.sql for how this differs from Artist.
type MonitoredArtist struct {
	ID           int64      `json:"id"`
	MBID         string     `json:"mbid"`
	Name         string     `json:"name"`
	SortName     string     `json:"sort_name"`
	AddedAt      time.Time  `json:"added_at"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
}

const monitoredArtistSelect = `SELECT id, mbid, name, sort_name, added_at, last_synced_at FROM monitored_artists`

func scanMonitoredArtist(row interface{ Scan(...any) error }) (*MonitoredArtist, error) {
	var m MonitoredArtist
	var lastSynced sql.NullTime
	if err := row.Scan(&m.ID, &m.MBID, &m.Name, &m.SortName, &m.AddedAt, &lastSynced); err != nil {
		return nil, err
	}
	if lastSynced.Valid {
		m.LastSyncedAt = &lastSynced.Time
	}
	return &m, nil
}

// CreateMonitoredArtist starts watching mbid. Fails (via the table's
// UNIQUE constraint) if it's already monitored.
func (db *DB) CreateMonitoredArtist(ctx context.Context, mbid, name, sortName string) (*MonitoredArtist, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO monitored_artists (mbid, name, sort_name, added_at) VALUES (?, ?, ?, ?)`,
		mbid, name, sortName, now)
	if err != nil {
		return nil, fmt.Errorf("insert monitored artist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &MonitoredArtist{ID: id, MBID: mbid, Name: name, SortName: sortName, AddedAt: now}, nil
}

// GetMonitoredArtist returns a single monitored artist by ID, or
// ErrNotFound.
func (db *DB) GetMonitoredArtist(ctx context.Context, id int64) (*MonitoredArtist, error) {
	m, err := scanMonitoredArtist(db.QueryRowContext(ctx, monitoredArtistSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get monitored artist: %w", err)
	}
	return m, nil
}

// ListMonitoredArtists returns every monitored artist, ordered by sort
// name.
func (db *DB) ListMonitoredArtists(ctx context.Context) ([]MonitoredArtist, error) {
	rows, err := db.QueryContext(ctx, monitoredArtistSelect+` ORDER BY sort_name`)
	if err != nil {
		return nil, fmt.Errorf("list monitored artists: %w", err)
	}
	defer rows.Close()

	out := []MonitoredArtist{}
	for rows.Next() {
		m, err := scanMonitoredArtist(rows)
		if err != nil {
			return nil, fmt.Errorf("scan monitored artist: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SetMonitoredArtistSynced records that id's wanted albums were just
// refreshed from MusicBrainz — see internal/acquisition's sync step.
func (db *DB) SetMonitoredArtistSynced(ctx context.Context, id int64, syncedAt time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE monitored_artists SET last_synced_at = ? WHERE id = ?`, syncedAt, id)
	if err != nil {
		return fmt.Errorf("set monitored artist synced: %w", err)
	}
	return nil
}

// DeleteMonitoredArtist stops watching id — its wanted_albums (and any
// in-flight downloads for them) cascade-delete with it.
func (db *DB) DeleteMonitoredArtist(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM monitored_artists WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete monitored artist: %w", err)
	}
	return nil
}
