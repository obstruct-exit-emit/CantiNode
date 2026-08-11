package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Album is a library album (MusicBrainz release), matched to a specific
// release MBID under ArtistID.
type Album struct {
	ID               int64     `json:"id"`
	ArtistID         int64     `json:"artistId"`
	MBID             string    `json:"mbid"`
	ReleaseGroupMBID string    `json:"releaseGroupMbid"`
	Title            string    `json:"title"`
	ReleaseDate      string    `json:"releaseDate"`
	PrimaryType      string    `json:"primaryType"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// GetOrCreateAlbum returns the existing album for artistID+releaseGroupMBID,
// inserting one if none exists yet. Album identity is the release GROUP —
// MusicBrainz's canonical album — not the specific release named by mbid:
// a Recording independently resolves to whichever of its own releases
// musicbrainz.Recording.BestRelease picks, so two tracks belonging to the
// very same physical album can easily carry two different release mbids.
// Deduplicating on release_group_mbid instead means they still collapse
// into one album row; whichever release's mbid/title/release_date/
// primary_type got recorded first is kept as-is on later calls, same as
// this package's other GetOrCreate* functions.
//
// releaseGroupMBID should never actually be empty (MusicBrainz's own
// release.ReleaseGroup.ID always is set), but falls back to the old
// mbid-keyed lookup defensively rather than risk duplicate rows if it
// ever is.
func (s *Store) GetOrCreateAlbum(artistID int64, mbid, releaseGroupMBID, title, releaseDate, primaryType string) (*Album, error) {
	var existing *Album
	var err error
	if releaseGroupMBID != "" {
		existing, err = s.getAlbumByReleaseGroupMBID(artistID, releaseGroupMBID)
	} else {
		existing, err = s.getAlbumByMBID(mbid)
	}
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
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

func (s *Store) getAlbumByMBID(mbid string) (*Album, error) {
	var a Album
	err := s.db.QueryRow(albumSelect+` WHERE mbid = ?`, mbid).
		Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album by mbid: %w", err)
	}
	return &a, nil
}

// getAlbumByReleaseGroupMBID mirrors getAlbumByMBID, scoped to the release
// group identity GetOrCreateAlbum now uses — see its doc comment.
func (s *Store) getAlbumByReleaseGroupMBID(artistID int64, releaseGroupMBID string) (*Album, error) {
	var a Album
	err := s.db.QueryRow(albumSelect+` WHERE artist_id = ? AND release_group_mbid = ?`, artistID, releaseGroupMBID).
		Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album by release group mbid: %w", err)
	}
	return &a, nil
}

// DeleteAlbum deletes id outright — the album page's own "Remove album"
// action, distinct from DeleteArtist's whole-discography version. Cascades
// (per the schema's own FK setup) to the album's tracks. Deliberately does
// NOT cascade to track_files, for the same reason DeleteArtist doesn't
// (see its own comment): track_files.track_id is ON DELETE SET NULL, not
// CASCADE, so calling this before every one of the album's own track_files
// rows has already been unlinked (via SetTrackFileMatch) would silently
// orphan them — track_id goes NULL but match_status stays whatever it was.
// RemoveAlbum is the only intended caller, and it does that cleanup first.
func (s *Store) DeleteAlbum(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM albums WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete album %d: %w", id, err)
	}
	return nil
}

const albumSelect = `SELECT id, artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at FROM albums`

// GetAlbum returns a single album by ID, or ErrNotFound.
func (s *Store) GetAlbum(id int64) (*Album, error) {
	var a Album
	err := s.db.QueryRow(albumSelect+` WHERE id = ?`, id).
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
func (s *Store) ListAlbumsByArtist(artistID int64) ([]Album, error) {
	rows, err := s.db.Query(`
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

	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see Store.ListArtists' identical note.
	out := []Album{}
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
