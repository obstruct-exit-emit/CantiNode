package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Track is a library track (MusicBrainz recording) under AlbumID.
type Track struct {
	ID          int64     `json:"id"`
	AlbumID     int64     `json:"albumId"`
	MBID        string    `json:"mbid"`
	Title       string    `json:"title"`
	TrackNumber int       `json:"trackNumber"`
	DiscNumber  int       `json:"discNumber"`
	DurationMs  int64     `json:"durationMs"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const trackSelect = `SELECT id, album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at FROM tracks`

// GetOrCreateTrack returns the existing track for mbid, inserting one if
// none exists yet.
func (s *Store) GetOrCreateTrack(albumID int64, mbid, title string, trackNumber, discNumber int, durationMs int64) (*Track, error) {
	existing, err := s.getTrackByMBID(mbid)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO tracks (album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		albumID, mbid, title, trackNumber, discNumber, durationMs, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert track: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Track{
		ID: id, AlbumID: albumID, MBID: mbid, Title: title,
		TrackNumber: trackNumber, DiscNumber: discNumber, DurationMs: durationMs,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) getTrackByMBID(mbid string) (*Track, error) {
	var t Track
	err := s.db.QueryRow(trackSelect+` WHERE mbid = ?`, mbid).
		Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track by mbid: %w", err)
	}
	return &t, nil
}

// GetTrack returns a single track by ID, or ErrNotFound.
func (s *Store) GetTrack(id int64) (*Track, error) {
	var t Track
	err := s.db.QueryRow(trackSelect+` WHERE id = ?`, id).
		Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track: %w", err)
	}
	return &t, nil
}

// ListTracksByAlbum returns every track under albumID, in disc/track order.
func (s *Store) ListTracksByAlbum(albumID int64) ([]Track, error) {
	rows, err := s.db.Query(trackSelect+` WHERE album_id = ? ORDER BY disc_number, track_number`, albumID)
	if err != nil {
		return nil, fmt.Errorf("list tracks by album: %w", err)
	}
	defer rows.Close()

	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see Store.ListArtists' identical note.
	out := []Track{}
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan track: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
