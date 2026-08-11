package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WantedStatus is a WantedAlbum's acquisition state. There's no "downloaded"
// terminal state: once internal/importer successfully imports a grab, the
// wanted_albums row is deleted outright rather than marked done — the
// album's ownership is what the real albums row (created by that same
// import) already represents, and ListMissingArtistReleaseGroups already
// excludes anything with one, so nothing needs the wanted row to linger.
type WantedStatus string

const (
	WantedStatusWanted      WantedStatus = "wanted"
	WantedStatusDownloading WantedStatus = "downloading"
)

// WantedAlbum is one release group CantiNode is trying to acquire for an
// Artist.
type WantedAlbum struct {
	ID               int64        `json:"id"`
	ArtistID         int64        `json:"artistId"`
	ReleaseGroupMBID string       `json:"releaseGroupMbid"`
	Title            string       `json:"title"`
	PrimaryType      string       `json:"primaryType"`
	ReleaseDate      string       `json:"releaseDate"`
	Status           WantedStatus `json:"status"`
	AddedAt          time.Time    `json:"addedAt"`
}

const wantedAlbumSelect = `SELECT id, artist_id, release_group_mbid, title, primary_type, release_date, status, added_at FROM wanted_albums`

func scanWantedAlbum(row interface{ Scan(...any) error }) (*WantedAlbum, error) {
	var w WantedAlbum
	if err := row.Scan(&w.ID, &w.ArtistID, &w.ReleaseGroupMBID, &w.Title, &w.PrimaryType, &w.ReleaseDate, &w.Status, &w.AddedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

// GetOrCreateWantedAlbum returns the existing wanted album for
// (artistID, releaseGroupMBID), inserting one (as WantedStatusWanted) if
// none exists yet — the discography-cache/want flow calls this once per
// release group the user picks, so it's naturally idempotent across
// repeated calls.
func (s *Store) GetOrCreateWantedAlbum(artistID int64, releaseGroupMBID, title, primaryType, releaseDate string) (*WantedAlbum, error) {
	existing, err := s.getWantedAlbumByReleaseGroup(artistID, releaseGroupMBID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO wanted_albums (artist_id, release_group_mbid, title, primary_type, release_date, status, added_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		artistID, releaseGroupMBID, title, primaryType, releaseDate, WantedStatusWanted, now)
	if err != nil {
		return nil, fmt.Errorf("insert wanted album: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &WantedAlbum{
		ID: id, ArtistID: artistID, ReleaseGroupMBID: releaseGroupMBID,
		Title: title, PrimaryType: primaryType, ReleaseDate: releaseDate,
		Status: WantedStatusWanted, AddedAt: now,
	}, nil
}

func (s *Store) getWantedAlbumByReleaseGroup(artistID int64, releaseGroupMBID string) (*WantedAlbum, error) {
	w, err := scanWantedAlbum(s.db.QueryRow(
		wantedAlbumSelect+` WHERE artist_id = ? AND release_group_mbid = ?`, artistID, releaseGroupMBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wanted album by release group: %w", err)
	}
	return w, nil
}

// GetWantedAlbum returns a single wanted album by ID, or ErrNotFound.
func (s *Store) GetWantedAlbum(id int64) (*WantedAlbum, error) {
	w, err := scanWantedAlbum(s.db.QueryRow(wantedAlbumSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wanted album: %w", err)
	}
	return w, nil
}

// ListWantedAlbumsByArtist returns every wanted album under artistID,
// newest release first.
func (s *Store) ListWantedAlbumsByArtist(artistID int64) ([]WantedAlbum, error) {
	rows, err := s.db.Query(wantedAlbumSelect+` WHERE artist_id = ? ORDER BY release_date DESC, title`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list wanted albums by artist: %w", err)
	}
	defer rows.Close()
	return scanWantedAlbumRows(rows)
}

// ListWantedAlbumsByStatus returns every wanted album currently in
// status, across every monitored artist — backs the Wanted list's own
// view (status=wanted) and the acquisition search loop.
func (s *Store) ListWantedAlbumsByStatus(status WantedStatus) ([]WantedAlbum, error) {
	rows, err := s.db.Query(wantedAlbumSelect+` WHERE status = ? ORDER BY added_at`, status)
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
func (s *Store) SetWantedAlbumStatus(id int64, status WantedStatus) error {
	_, err := s.db.Exec(`UPDATE wanted_albums SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("set wanted album status: %w", err)
	}
	return nil
}

// ClaimWantedAlbumForDownload atomically flips id from 'wanted' to
// 'downloading' — a compare-and-swap, not a blind write, so two callers
// racing to grab the same wanted album (a manual "Grab" click and the
// automatic wanted-list sweep firing at the same moment, say) can't both
// succeed: only the first UPDATE actually matches a row, so only one
// caller sees claimed=true and should proceed to grab; the other sees
// false and must not. Callers grab only after claiming, not before —
// claiming first is what makes the race actually closed rather than just
// narrowed.
func (s *Store) ClaimWantedAlbumForDownload(id int64) (claimed bool, err error) {
	res, err := s.db.Exec(`UPDATE wanted_albums SET status = ? WHERE id = ? AND status = ?`,
		WantedStatusDownloading, id, WantedStatusWanted)
	if err != nil {
		return false, fmt.Errorf("claim wanted album for download: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim wanted album for download: %w", err)
	}
	return n > 0, nil
}

// DeleteWantedAlbum removes a wanted album entirely — the "no longer
// wanted" action. Unlike a status change, this actually frees the release
// group back up: ListMissingArtistReleaseGroups excludes a release group
// for as long as any wanted_albums row references it, so leaving a row
// behind (e.g. under a former "ignored" status) would strand the album
// forever, showing in neither Wanted nor Missing.
func (s *Store) DeleteWantedAlbum(id int64) error {
	res, err := s.db.Exec(`DELETE FROM wanted_albums WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete wanted album: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
