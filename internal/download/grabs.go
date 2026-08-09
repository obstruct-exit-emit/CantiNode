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
	BookID         int64  `json:"bookId,omitempty"`
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

const grabCols = `id, COALESCE(book_id, 0), COALESCE(client_config_id, 0), client_item_id,
	title, guid, protocol, media_type, status, message, grabbed_at, COALESCE(completed_at, '')`

func scanGrab(row interface{ Scan(...any) error }) (*GrabRecord, error) {
	var g GrabRecord
	err := row.Scan(&g.ID, &g.BookID, &g.ClientConfigID, &g.ClientItemID,
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
	bookID := sql.NullInt64{Int64: g.BookID, Valid: g.BookID > 0}
	configID := sql.NullInt64{Int64: g.ClientConfigID, Valid: g.ClientConfigID > 0}
	if g.Status == "" {
		g.Status = GrabStatusGrabbed
	}
	if g.MediaType == "" {
		g.MediaType = "ebook"
	}
	return s.db.QueryRow(`
		INSERT INTO grabs (book_id, client_config_id, client_item_id, title, guid, protocol, media_type, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, grabbed_at`,
		bookID, configID, g.ClientItemID, g.Title, g.GUID, g.Protocol, g.MediaType, g.Status,
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
