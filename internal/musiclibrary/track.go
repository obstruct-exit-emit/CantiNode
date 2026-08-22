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
	// ArtistCredit is this specific track's own performing-artist credit
	// from MusicBrainz — distinct from the album's own artist, and the
	// whole point of storing it separately: on a "Various Artists" album,
	// every track shares the same (Various Artists) album artist but each
	// has its own real performer (e.g. "Phil Collins", "Duran Duran").
	// Empty whenever it matches the album's own artist and so adds
	// nothing worth displaying — see AlbumDetailView's own rendering.
	ArtistCredit string `json:"artistCredit,omitempty"`
	// ArtistCreditMBID is ArtistCredit's own MusicBrainz artist ID — the
	// same distinction, for the ID rather than the display name: on a
	// Various Artists album, this is the track's own real performer's ID,
	// not the album's shared filing artist. Empty under the same rule as
	// ArtistCredit (matches the album's own artist, nothing extra to
	// carry). Written into a file's own "MusicBrainz Artist Id" tag
	// instead of the album artist's — see internal/tagwriter.
	ArtistCreditMBID string `json:"artistCreditMbid,omitempty"`
}

const trackSelect = `SELECT id, album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at, artist_credit, artist_credit_mbid FROM tracks`

// GetOrCreateTrack returns albumID's existing track for mbid, inserting
// one if none exists yet under this album specifically. Every field
// except artistCredit/artistCreditMBID is only ever stored at insert time
// — an existing track keeps its own title/track number/disc number/
// duration regardless of what a later match call passes in.
//
// artistCredit/artistCreditMBID are the exception: an existing row gets
// them refreshed to whatever this call resolved, whenever they differ.
// Found live: these two started out "insert-time only" like everything
// else, so a track matched before artist_credit_mbid existed (or before
// applyMatch correctly resolved it — see migration 031) stayed wrong
// forever, since nothing short of deleting and recreating the row would
// ever update it. Re-matching (an explicit clear+rematch, or any future
// resolution logic getting smarter) now actually corrects a track's own
// stored display credit/ID instead of leaving a stale first-match value
// stuck in place indefinitely.
//
// Scoped to (albumID, mbid), not mbid alone — found live: the same
// MusicBrainz recording legitimately appearing on two different releases
// (a single also included on its parent album) used to collapse onto one
// globally-shared track row, so a file correctly matched and organized
// under one release could never get its own row under the other; Organize
// then refused to place a second file at that row's already-occupied
// destination, with nothing in the error connecting it back to this
// cause. One recording can now have one track row per album it actually
// belongs to — mirrors idx_albums_artist_release_group's own per-artist
// scoping for the identical reason (see migration 030).
func (s *Store) GetOrCreateTrack(albumID int64, mbid, title string, trackNumber, discNumber int, durationMs int64, artistCredit, artistCreditMBID string) (*Track, error) {
	existing, err := s.getTrackByAlbumAndMBID(albumID, mbid)
	if err == nil {
		if existing.ArtistCredit != artistCredit || existing.ArtistCreditMBID != artistCreditMBID {
			now := time.Now().UTC()
			if _, err := s.db.Exec(
				`UPDATE tracks SET artist_credit = ?, artist_credit_mbid = ?, updated_at = ? WHERE id = ?`,
				artistCredit, artistCreditMBID, now, existing.ID); err != nil {
				return nil, fmt.Errorf("refresh track artist credit: %w", err)
			}
			existing.ArtistCredit = artistCredit
			existing.ArtistCreditMBID = artistCreditMBID
			existing.UpdatedAt = now
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO tracks (album_id, mbid, title, track_number, disc_number, duration_ms, created_at, updated_at, artist_credit, artist_credit_mbid)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		albumID, mbid, title, trackNumber, discNumber, durationMs, now, now, artistCredit, artistCreditMBID)
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
		CreatedAt: now, UpdatedAt: now, ArtistCredit: artistCredit, ArtistCreditMBID: artistCreditMBID,
	}, nil
}

func (s *Store) getTrackByAlbumAndMBID(albumID int64, mbid string) (*Track, error) {
	var t Track
	err := s.db.QueryRow(trackSelect+` WHERE album_id = ? AND mbid = ?`, albumID, mbid).
		Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt, &t.ArtistCredit, &t.ArtistCreditMBID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get track by album and mbid: %w", err)
	}
	return &t, nil
}

// GetTrack returns a single track by ID, or ErrNotFound.
func (s *Store) GetTrack(id int64) (*Track, error) {
	var t Track
	err := s.db.QueryRow(trackSelect+` WHERE id = ?`, id).
		Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt, &t.ArtistCredit, &t.ArtistCreditMBID)
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
		if err := rows.Scan(&t.ID, &t.AlbumID, &t.MBID, &t.Title, &t.TrackNumber, &t.DiscNumber, &t.DurationMs, &t.CreatedAt, &t.UpdatedAt, &t.ArtistCredit, &t.ArtistCreditMBID); err != nil {
			return nil, fmt.Errorf("scan track: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
