package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Playlist is a user-curated ordered list of tracks, independent of any
// album/artist — see migration 034's own comment on why items reference
// tracks rather than track_files.
type Playlist struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	TrackCount      int       `json:"trackCount"`
	TotalDurationMs int64     `json:"totalDurationMs"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PlaylistTrack is one playlist_items row, joined out to what a UI (or the
// M3U exporter) actually needs to show/use it: the track's own display
// info, the album it belongs to (its artist is the one shown/linked — see
// AlbumDetailView's identical convention — ArtistCredit is a supplementary
// "featuring" credit, never a replacement for it), and whichever
// track_file currently backs it, if any.
type PlaylistTrack struct {
	ItemID       int64  `json:"itemId"`
	TrackID      int64  `json:"trackId"`
	Position     int    `json:"position"`
	Title        string `json:"title"`
	DurationMs   int64  `json:"durationMs"`
	ArtistCredit string `json:"artistCredit,omitempty"`
	ArtistID     int64  `json:"artistId"`
	ArtistName   string `json:"artistName"`
	AlbumID      int64  `json:"albumId"`
	AlbumTitle   string `json:"albumTitle"`
	// TrackFileID/Path are zero/empty when nothing currently backs this
	// track (deleted, never matched, or the match was cleared) — still a
	// real playlist entry, just not exportable/playable until it's owned
	// again.
	TrackFileID int64  `json:"trackFileId,omitempty"`
	Path        string `json:"path,omitempty"`
}

const playlistTrackSelect = `
	SELECT pi.id, pi.track_id, pi.position,
	       t.title, t.duration_ms, t.artist_credit,
	       al.artist_id, ar.name, al.id, al.title,
	       COALESCE(tf.id, 0), COALESCE(tf.path, '')
	FROM playlist_items pi
	JOIN tracks t ON t.id = pi.track_id
	JOIN albums al ON al.id = t.album_id
	JOIN artists ar ON ar.id = al.artist_id
	LEFT JOIN track_files tf ON tf.id = (SELECT MIN(id) FROM track_files WHERE track_id = t.id)`

func scanPlaylistTrack(row interface{ Scan(...any) error }) (PlaylistTrack, error) {
	var pt PlaylistTrack
	err := row.Scan(&pt.ItemID, &pt.TrackID, &pt.Position, &pt.Title, &pt.DurationMs, &pt.ArtistCredit,
		&pt.ArtistID, &pt.ArtistName, &pt.AlbumID, &pt.AlbumTitle, &pt.TrackFileID, &pt.Path)
	return pt, err
}

// CreatePlaylist makes a new, empty playlist.
func (s *Store) CreatePlaylist(name, description string) (*Playlist, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO playlists (name, description, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		name, description, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert playlist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Playlist{ID: id, Name: name, Description: description, CreatedAt: now, UpdatedAt: now}, nil
}

const playlistSummarySelectBase = `
	SELECT p.id, p.name, p.description, p.created_at, p.updated_at,
	       COUNT(pi.id), COALESCE(SUM(t.duration_ms), 0)
	FROM playlists p
	LEFT JOIN playlist_items pi ON pi.playlist_id = p.id
	LEFT JOIN tracks t ON t.id = pi.track_id`

// ListPlaylists returns every playlist with its track count and total
// duration, alphabetically.
func (s *Store) ListPlaylists() ([]Playlist, error) {
	rows, err := s.db.Query(playlistSummarySelectBase + ` GROUP BY p.id ORDER BY p.name`)
	if err != nil {
		return nil, fmt.Errorf("list playlists: %w", err)
	}
	defer rows.Close()

	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.TrackCount, &p.TotalDurationMs); err != nil {
			return nil, fmt.Errorf("scan playlist: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlaylist returns one playlist by id, with the same track count/total
// duration ListPlaylists reports.
func (s *Store) GetPlaylist(id int64) (*Playlist, error) {
	var p Playlist
	err := s.db.QueryRow(playlistSummarySelectBase+` WHERE p.id = ? GROUP BY p.id`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.TrackCount, &p.TotalDurationMs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get playlist: %w", err)
	}
	return &p, nil
}

// UpdatePlaylist renames/redescribes a playlist.
func (s *Store) UpdatePlaylist(id int64, name, description string) error {
	res, err := s.db.Exec(`UPDATE playlists SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		name, description, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update playlist: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePlaylist removes a playlist and every one of its items (cascade) —
// never touches the tracks or files it pointed at.
func (s *Store) DeletePlaylist(id int64) error {
	res, err := s.db.Exec(`DELETE FROM playlists WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete playlist: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPlaylistTracks returns playlistID's tracks in order.
func (s *Store) ListPlaylistTracks(playlistID int64) ([]PlaylistTrack, error) {
	rows, err := s.db.Query(playlistTrackSelect+` WHERE pi.playlist_id = ? ORDER BY pi.position`, playlistID)
	if err != nil {
		return nil, fmt.Errorf("list playlist tracks: %w", err)
	}
	defer rows.Close()

	out := []PlaylistTrack{}
	for rows.Next() {
		pt, err := scanPlaylistTrack(rows)
		if err != nil {
			return nil, fmt.Errorf("scan playlist track: %w", err)
		}
		out = append(out, pt)
	}
	return out, rows.Err()
}

// AppendPlaylistItem adds trackID to the end of playlistID.
func (s *Store) AppendPlaylistItem(playlistID, trackID int64) (*PlaylistTrack, error) {
	var exists int64
	if err := s.db.QueryRow(`SELECT id FROM playlists WHERE id = ?`, playlistID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("check playlist: %w", err)
	}

	var maxPos sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&maxPos); err != nil {
		return nil, fmt.Errorf("max position: %w", err)
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO playlist_items (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?)`,
		playlistID, trackID, int(maxPos.Int64)+1, now)
	if err != nil {
		return nil, fmt.Errorf("insert playlist item: %w", err)
	}
	itemID, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, now, playlistID); err != nil {
		return nil, fmt.Errorf("touch playlist: %w", err)
	}

	pt, err := scanPlaylistTrack(s.db.QueryRow(playlistTrackSelect+` WHERE pi.id = ?`, itemID))
	if err != nil {
		return nil, fmt.Errorf("read back playlist item: %w", err)
	}
	return &pt, nil
}

// RemovePlaylistItem removes one item by its own id — not by track id,
// since the same track may appear in a playlist more than once.
func (s *Store) RemovePlaylistItem(playlistID, itemID int64) error {
	res, err := s.db.Exec(`DELETE FROM playlist_items WHERE id = ? AND playlist_id = ?`, itemID, playlistID)
	if err != nil {
		return fmt.Errorf("remove playlist item: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, time.Now().UTC(), playlistID)
	if err != nil {
		return fmt.Errorf("touch playlist: %w", err)
	}
	return nil
}

// ReorderPlaylistItems sets playlistID's item order to itemIDs — the
// dropped position of a drag-reorder, given as the item ids' whole new
// order. Every id must already belong to playlistID; any that doesn't
// fails the whole reorder rather than silently reassigning someone else's
// item.
func (s *Store) ReorderPlaylistItems(playlistID int64, itemIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for i, itemID := range itemIDs {
		res, err := tx.Exec(`UPDATE playlist_items SET position = ? WHERE id = ? AND playlist_id = ?`, i, itemID, playlistID)
		if err != nil {
			return fmt.Errorf("update position: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("item %d is not in playlist %d", itemID, playlistID)
		}
	}
	if _, err := tx.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, time.Now().UTC(), playlistID); err != nil {
		return fmt.Errorf("touch playlist: %w", err)
	}
	return tx.Commit()
}
