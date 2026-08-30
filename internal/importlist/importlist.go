// Package importlist periodically resolves configured external sources
// (a MusicBrainz Series, a plain artist list, or a Last.fm user/tag's top
// artists) into MusicBrainz artist MBIDs, adding and monitoring any new
// one automatically — joining the existing internal/autosearch wanted-list
// sweep with no manual "+Add" step. Add-only: an artist that later falls
// off a list stays in the library, matching CantiNode's existing
// "never auto-delete" posture elsewhere (see internal/autosearch's own
// package doc comment).
package importlist

import (
	"database/sql"
	"errors"
)

// Import-list source types.
const (
	TypeMusicBrainzSeries = "musicbrainz_series"
	TypeList              = "list"
	TypeLastFM            = "lastfm"
)

// Last.fm source kinds — see ImportList.LastfmKind.
const (
	LastfmKindUser = "user"
	LastfmKindTag  = "tag"
)

// ErrNotFound is returned when a requested import list does not exist.
var ErrNotFound = errors.New("import list not found")

// ImportList is one configured external source.
type ImportList struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// SeriesMBID: the MusicBrainz series MBID, for Type == musicbrainz_series.
	SeriesMBID string `json:"seriesMbid"`
	// ListText: pasted text, one artist name per line, for Type == list —
	// used directly when SourceURL is empty.
	ListText string `json:"listText"`
	// SourceURL: fetched fresh on every sync instead of ListText, same
	// one-line-per-artist shape, for Type == list.
	SourceURL string `json:"sourceUrl"`
	// LastfmKind picks whether LastfmTarget names a Last.fm username (that
	// user's top artists) or a tag/genre (that tag's top artists), for
	// Type == lastfm.
	LastfmKind    string `json:"lastfmKind"`
	LastfmTarget  string `json:"lastfmTarget"`
	Enabled       bool   `json:"enabled"`
	AddedAt       string `json:"addedAt"`
	LastSyncedAt  string `json:"lastSyncedAt,omitempty"`
	LastSyncError string `json:"lastSyncError,omitempty"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const cols = `id, name, type, series_mbid, list_text, source_url, lastfm_kind, lastfm_target, enabled, added_at, last_synced_at, last_sync_error`

func scanImportList(row interface{ Scan(...any) error }) (*ImportList, error) {
	var l ImportList
	var lastSyncedAt sql.NullString
	err := row.Scan(&l.ID, &l.Name, &l.Type, &l.SeriesMBID, &l.ListText, &l.SourceURL,
		&l.LastfmKind, &l.LastfmTarget, &l.Enabled, &l.AddedAt, &lastSyncedAt, &l.LastSyncError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.LastSyncedAt = lastSyncedAt.String
	return &l, nil
}

// normalizeLastfmKind defaults an unset LastfmKind to "user" — the zero
// Go value for a type that doesn't care (musicbrainz_series/list) would
// otherwise violate the lastfm_kind CHECK constraint, which only accepts
// 'user'/'tag'.
func normalizeLastfmKind(l *ImportList) {
	if l.LastfmKind == "" {
		l.LastfmKind = LastfmKindUser
	}
}

func (s *Store) Add(l *ImportList) error {
	normalizeLastfmKind(l)
	return s.db.QueryRow(`
		INSERT INTO import_lists (name, type, series_mbid, list_text, source_url, lastfm_kind, lastfm_target, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, added_at`,
		l.Name, l.Type, l.SeriesMBID, l.ListText, l.SourceURL, l.LastfmKind, l.LastfmTarget, l.Enabled,
	).Scan(&l.ID, &l.AddedAt)
}

func (s *Store) Update(l *ImportList) error {
	normalizeLastfmKind(l)
	res, err := s.db.Exec(`
		UPDATE import_lists
		SET name = ?, type = ?, series_mbid = ?, list_text = ?, source_url = ?, lastfm_kind = ?, lastfm_target = ?, enabled = ?
		WHERE id = ?`,
		l.Name, l.Type, l.SeriesMBID, l.ListText, l.SourceURL, l.LastfmKind, l.LastfmTarget, l.Enabled, l.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Get(id int64) (*ImportList, error) {
	return scanImportList(s.db.QueryRow(`SELECT `+cols+` FROM import_lists WHERE id = ?`, id))
}

func (s *Store) List() ([]ImportList, error) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM import_lists ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lists := []ImportList{}
	for rows.Next() {
		l, err := scanImportList(rows)
		if err != nil {
			return nil, err
		}
		lists = append(lists, *l)
	}
	return lists, rows.Err()
}

func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM import_lists WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSyncResult records the outcome of one sync attempt — syncErr is
// stored empty on success, so the Settings list can show either a
// timestamp alone or the timestamp plus a reason for the last failure.
func (s *Store) SetSyncResult(id int64, syncedAt string, syncErr string) error {
	_, err := s.db.Exec(`UPDATE import_lists SET last_synced_at = ?, last_sync_error = ? WHERE id = ?`, syncedAt, syncErr, id)
	return err
}
