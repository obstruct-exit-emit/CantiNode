package download

import (
	"database/sql"
	"errors"
	"strings"
)

// Grab statuses.
const (
	GrabStatusGrabbed  = "grabbed"
	GrabStatusImported = "imported"
	GrabStatusFailed   = "failed"
)

// GrabRecord tracks one release sent to a download client and its outcome.
type GrabRecord struct {
	ID             int64  `json:"id"`
	WantedAlbumID  int64  `json:"wantedAlbumId,omitempty"`
	UpgradeAlbumID int64  `json:"upgradeAlbumId,omitempty"`
	ClientConfigID int64  `json:"clientConfigId,omitempty"`
	ClientItemID   string `json:"clientItemId,omitempty"`
	Title          string `json:"title"`
	GUID           string `json:"guid,omitempty"` // release guid, for the blocklist
	Protocol       string `json:"protocol"`
	MediaType      string `json:"mediaType"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`
	GrabbedAt      string `json:"grabbedAt"`
	CompletedAt    string `json:"completedAt,omitempty"`
}

const grabCols = `id, COALESCE(wanted_album_id, 0), COALESCE(upgrade_album_id, 0), COALESCE(client_config_id, 0), client_item_id,
	title, guid, protocol, media_type, status, message, grabbed_at, COALESCE(completed_at, '')`

func scanGrab(row interface{ Scan(...any) error }) (*GrabRecord, error) {
	var g GrabRecord
	err := row.Scan(&g.ID, &g.WantedAlbumID, &g.UpgradeAlbumID, &g.ClientConfigID, &g.ClientItemID,
		&g.Title, &g.GUID, &g.Protocol, &g.MediaType, &g.Status, &g.Message, &g.GrabbedAt, &g.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// AddGrab records a release sent to a client.
func (s *Store) AddGrab(g *GrabRecord) error {
	wantedAlbumID := sql.NullInt64{Int64: g.WantedAlbumID, Valid: g.WantedAlbumID > 0}
	upgradeAlbumID := sql.NullInt64{Int64: g.UpgradeAlbumID, Valid: g.UpgradeAlbumID > 0}
	configID := sql.NullInt64{Int64: g.ClientConfigID, Valid: g.ClientConfigID > 0}
	if g.Status == "" {
		g.Status = GrabStatusGrabbed
	}
	if g.MediaType == "" {
		g.MediaType = "music"
	}
	return s.db.QueryRow(`
		INSERT INTO grabs (wanted_album_id, upgrade_album_id, client_config_id, client_item_id, title, guid, protocol, media_type, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, grabbed_at`,
		wantedAlbumID, upgradeAlbumID, configID, g.ClientItemID, g.Title, g.GUID, g.Protocol, g.MediaType, g.Status,
	).Scan(&g.ID, &g.GrabbedAt)
}

// GrabHistory returns grab history newest first with paging and an optional
// case-insensitive title filter; the second return is the total matching
// count so the UI can page through a busy instance's full history.
func (s *Store) GrabHistory(search string, limit, offset int) ([]GrabRecord, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where := ""
	args := []any{}
	if search != "" {
		where = ` WHERE title LIKE ? ESCAPE '\' COLLATE NOCASE`
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
		args = append(args, "%"+esc+"%")
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM grabs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT `+grabCols+` FROM grabs`+where+` ORDER BY grabbed_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	grabs := []GrabRecord{}
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, 0, err
		}
		grabs = append(grabs, *g)
	}
	return grabs, total, rows.Err()
}

// GetGrab returns a single grab's current row, ErrNotFound if it no longer
// exists — used to re-check a grab's live status mid-import, since the
// GrabRecord a caller is already holding can go stale the moment another
// request resolves the same grab out from under it.
func (s *Store) GetGrab(id int64) (*GrabRecord, error) {
	row := s.db.QueryRow(`SELECT `+grabCols+` FROM grabs WHERE id = ?`, id)
	return scanGrab(row)
}

// ListGrabs returns grab history, optionally filtered by status, newest first.
func (s *Store) ListGrabs(status string) ([]GrabRecord, error) {
	query := `SELECT ` + grabCols + ` FROM grabs`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY grabbed_at DESC, id DESC LIMIT 200`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grabs := []GrabRecord{}
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, err
		}
		grabs = append(grabs, *g)
	}
	return grabs, rows.Err()
}

// ListGrabsForWantedAlbums returns every grab under the given status tied
// to one of wantedAlbumIDs — unlike ListGrabs, not capped at 200 rows,
// since a caller here already knows exactly which rows it wants (e.g.
// canceling in-flight grabs for an artist/album about to be removed) and
// needs all of them, not just the most recent 200 in-flight grabs
// instance-wide. Returns an empty slice for an empty wantedAlbumIDs
// without touching the database.
func (s *Store) ListGrabsForWantedAlbums(wantedAlbumIDs []int64, status string) ([]GrabRecord, error) {
	if len(wantedAlbumIDs) == 0 {
		return []GrabRecord{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(wantedAlbumIDs)), ",")
	args := make([]any, 0, len(wantedAlbumIDs)+1)
	for _, id := range wantedAlbumIDs {
		args = append(args, id)
	}
	args = append(args, status)

	rows, err := s.db.Query(
		`SELECT `+grabCols+` FROM grabs WHERE wanted_album_id IN (`+placeholders+`) AND status = ? ORDER BY grabbed_at DESC, id DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	grabs := []GrabRecord{}
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, err
		}
		grabs = append(grabs, *g)
	}
	return grabs, rows.Err()
}

// ResolveGrab marks a grab imported or failed.
func (s *Store) ResolveGrab(id int64, status, message string) error {
	res, err := s.db.Exec(`
		UPDATE grabs SET status = ?, message = ?, completed_at = datetime('now')
		WHERE id = ?`, status, message, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearHistory deletes every resolved grab (imported or failed) — a still
// in-flight one (status=grabbed) is left alone, since it's active tracking
// the importer still needs, not history yet. Returns how many rows were
// removed.
func (s *Store) ClearHistory() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM grabs WHERE status != ?`, GrabStatusGrabbed)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
