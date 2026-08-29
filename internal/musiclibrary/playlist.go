package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// ErrTrackNotFound means an append referenced a track id that doesn't
// exist — distinct from ErrNotFound (which here always means the
// *playlist* itself is missing): the URL's own resource is fine, the
// request body's content isn't. Found live: without this check, a bad
// track id fell all the way through to a raw SQLite foreign-key-
// constraint error surfacing as an unhandled 500.
var ErrTrackNotFound = errors.New("musiclibrary: track not found")

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

	var trackExists int64
	if err := s.db.QueryRow(`SELECT id FROM tracks WHERE id = ?`, trackID).Scan(&trackExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %d", ErrTrackNotFound, trackID)
		}
		return nil, fmt.Errorf("check track: %w", err)
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

// AppendPlaylistItems adds every trackID to the end of playlistID, in the
// order given, in one transaction — the album-page "add whole album" and
// M3U import both go through this rather than looping single appends, so
// a partial failure never leaves half an album added.
func (s *Store) AppendPlaylistItems(playlistID int64, trackIDs []int64) ([]PlaylistTrack, error) {
	if len(trackIDs) == 0 {
		return []PlaylistTrack{}, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var exists int64
	if err := tx.QueryRow(`SELECT id FROM playlists WHERE id = ?`, playlistID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("check playlist: %w", err)
	}
	placeholders := make([]string, len(trackIDs))
	args := make([]any, len(trackIDs))
	for i, id := range trackIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := tx.Query(`SELECT id FROM tracks WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("check tracks: %w", err)
	}
	found := make(map[int64]bool, len(trackIDs))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan track id: %w", err)
		}
		found[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("check tracks: %w", err)
	}
	for _, id := range trackIDs {
		if !found[id] {
			return nil, fmt.Errorf("%w: %d", ErrTrackNotFound, id)
		}
	}

	var maxPos sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(position) FROM playlist_items WHERE playlist_id = ?`, playlistID).Scan(&maxPos); err != nil {
		return nil, fmt.Errorf("max position: %w", err)
	}
	now := time.Now().UTC()
	pos := int(maxPos.Int64)
	itemIDs := make([]int64, 0, len(trackIDs))
	for _, trackID := range trackIDs {
		pos++
		res, err := tx.Exec(`INSERT INTO playlist_items (playlist_id, track_id, position, added_at) VALUES (?, ?, ?, ?)`,
			playlistID, trackID, pos, now)
		if err != nil {
			return nil, fmt.Errorf("insert playlist item: %w", err)
		}
		itemID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("last insert id: %w", err)
		}
		itemIDs = append(itemIDs, itemID)
	}
	if _, err := tx.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, now, playlistID); err != nil {
		return nil, fmt.Errorf("touch playlist: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	out := make([]PlaylistTrack, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		pt, err := scanPlaylistTrack(s.db.QueryRow(playlistTrackSelect+` WHERE pi.id = ?`, itemID))
		if err != nil {
			return nil, fmt.Errorf("read back playlist item: %w", err)
		}
		out = append(out, pt)
	}
	return out, nil
}

// ImportM3UResult reports what an M3U import actually did.
type ImportM3UResult struct {
	Playlist *Playlist `json:"playlist"`
	Imported int       `json:"imported"`
	Skipped  int       `json:"skipped"`
}

// ImportPlaylistFromM3U creates a new playlist named name from an M3U
// file's content: every non-comment, non-blank line is a path, resolved
// against this library's own track_files by an exact match. A line that
// doesn't resolve (not this library's own export, moved since, or from a
// different library entirely) is silently skipped and counted, rather
// than failing the whole import — a playlist with 8 of 10 tracks
// recovered is still useful.
func (s *Store) ImportPlaylistFromM3U(name, content string) (*ImportM3UResult, error) {
	var trackIDs []int64
	skipped := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		tf, err := s.getTrackFileByPath(line)
		if err != nil || tf.TrackID == nil {
			skipped++
			continue
		}
		trackIDs = append(trackIDs, *tf.TrackID)
	}

	p, err := s.CreatePlaylist(name, "")
	if err != nil {
		return nil, err
	}
	if len(trackIDs) > 0 {
		if _, err := s.AppendPlaylistItems(p.ID, trackIDs); err != nil {
			return nil, err
		}
		if p, err = s.GetPlaylist(p.ID); err != nil {
			return nil, err
		}
	}
	return &ImportM3UResult{Playlist: p, Imported: len(trackIDs), Skipped: skipped}, nil
}

// TrackSearchResult is one owned track matching a title search, joined the
// same way PlaylistTrack is — the Search page's track-level results. Only
// a track with a real current file is worth surfacing here: the whole
// point of finding it is adding it to a playlist that can actually use it.
type TrackSearchResult struct {
	TrackID      int64  `json:"trackId"`
	Title        string `json:"title"`
	DurationMs   int64  `json:"durationMs"`
	ArtistCredit string `json:"artistCredit,omitempty"`
	ArtistID     int64  `json:"artistId"`
	ArtistName   string `json:"artistName"`
	AlbumID      int64  `json:"albumId"`
	AlbumTitle   string `json:"albumTitle"`
	TrackFileID  int64  `json:"trackFileId"`
}

// likeEscaper escapes the characters SQLite's LIKE treats specially ('%',
// '_', and the escape character itself) so a query built with them via
// ESCAPE '\' matches only literally — e.g. a track title containing a
// literal underscore shouldn't act as a single-character wildcard.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchOwnedTracks finds owned, file-backed tracks whose title contains
// query (SQLite's LIKE is case-insensitive for ASCII by default), most
// recently added first.
func (s *Store) SearchOwnedTracks(query string, limit int) ([]TrackSearchResult, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.title, t.duration_ms, t.artist_credit,
		       al.artist_id, ar.name, al.id, al.title, tf.id
		FROM tracks t
		JOIN albums al ON al.id = t.album_id
		JOIN artists ar ON ar.id = al.artist_id
		JOIN track_files tf ON tf.id = (SELECT MIN(id) FROM track_files WHERE track_id = t.id)
		WHERE t.title LIKE '%' || ? || '%' ESCAPE '\'
		ORDER BY t.id DESC
		LIMIT ?`, likeEscaper.Replace(query), limit)
	if err != nil {
		return nil, fmt.Errorf("search owned tracks: %w", err)
	}
	defer rows.Close()

	out := []TrackSearchResult{}
	for rows.Next() {
		var r TrackSearchResult
		if err := rows.Scan(&r.TrackID, &r.Title, &r.DurationMs, &r.ArtistCredit,
			&r.ArtistID, &r.ArtistName, &r.AlbumID, &r.AlbumTitle, &r.TrackFileID); err != nil {
			return nil, fmt.Errorf("scan track search result: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPlaylistsForTrack returns every playlist containing trackID, each
// with its own full track count/duration (not filtered to just this
// track) — the "in playlist" badge's own detail view.
func (s *Store) ListPlaylistsForTrack(trackID int64) ([]Playlist, error) {
	rows, err := s.db.Query(playlistSummarySelectBase+`
		WHERE p.id IN (SELECT playlist_id FROM playlist_items WHERE track_id = ?)
		GROUP BY p.id ORDER BY p.name`, trackID)
	if err != nil {
		return nil, fmt.Errorf("list playlists for track: %w", err)
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

// TracksInAnyPlaylist reports which of trackIDs appear in at least one
// playlist — a track's own detail rows use this to flag "already in a
// playlist" without a per-track round trip. Absent from the returned map
// (rather than present-and-false) for any id in no playlist.
func (s *Store) TracksInAnyPlaylist(trackIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(trackIDs))
	if len(trackIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(trackIDs))
	args := make([]any, len(trackIDs))
	for i, id := range trackIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT track_id FROM playlist_items WHERE track_id IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("tracks in any playlist: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan track id: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
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
// order. itemIDs must name every item currently in the playlist, each
// exactly once — a partial or stale list (a racing delete, a second tab)
// is rejected outright rather than applied, which would leave the omitted
// item's old position value in place and free to collide with a position
// this call just assigned to a different item.
func (s *Store) ReorderPlaylistItems(playlistID int64, itemIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	current, err := tx.Query(`SELECT id FROM playlist_items WHERE playlist_id = ?`, playlistID)
	if err != nil {
		return fmt.Errorf("list current items: %w", err)
	}
	want := make(map[int64]bool)
	for current.Next() {
		var id int64
		if err := current.Scan(&id); err != nil {
			current.Close()
			return fmt.Errorf("scan current item: %w", err)
		}
		want[id] = true
	}
	if err := current.Err(); err != nil {
		return fmt.Errorf("list current items: %w", err)
	}
	current.Close()

	if len(itemIDs) != len(want) {
		return fmt.Errorf("reorder must include every item in the playlist: got %d, want %d", len(itemIDs), len(want))
	}
	seen := make(map[int64]bool, len(itemIDs))
	for _, id := range itemIDs {
		if seen[id] {
			return fmt.Errorf("item %d listed more than once", id)
		}
		if !want[id] {
			return fmt.Errorf("item %d is not in playlist %d", id, playlistID)
		}
		seen[id] = true
	}

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
