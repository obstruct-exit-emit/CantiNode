package indexer

import (
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a requested indexer does not exist.
var ErrNotFound = errors.New("indexer not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const cols = `id, name, type, base_url, api_key, categories, audio_categories, comic_categories, magazine_categories, enabled, priority, added_at`

func scanIndexer(row interface{ Scan(...any) error }) (*Indexer, error) {
	var i Indexer
	err := row.Scan(&i.ID, &i.Name, &i.Type, &i.BaseURL, &i.APIKey,
		&i.Categories, &i.AudioCategories, &i.ComicCategories, &i.MagazineCategories, &i.Enabled, &i.Priority, &i.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func (s *Store) Add(i *Indexer) error {
	return s.db.QueryRow(`
		INSERT INTO indexers (name, type, base_url, api_key, categories, audio_categories, comic_categories, magazine_categories, enabled, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, added_at`,
		i.Name, i.Type, i.BaseURL, i.APIKey, i.Categories, i.AudioCategories, i.ComicCategories, i.MagazineCategories, i.Enabled, i.Priority,
	).Scan(&i.ID, &i.AddedAt)
}

func (s *Store) Update(i *Indexer) error {
	res, err := s.db.Exec(`
		UPDATE indexers
		SET name = ?, type = ?, base_url = ?, api_key = ?, categories = ?, audio_categories = ?, comic_categories = ?, magazine_categories = ?, enabled = ?, priority = ?
		WHERE id = ?`,
		i.Name, i.Type, i.BaseURL, i.APIKey, i.Categories, i.AudioCategories, i.ComicCategories, i.MagazineCategories, i.Enabled, i.Priority, i.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Get(id int64) (*Indexer, error) {
	return scanIndexer(s.db.QueryRow(`SELECT `+cols+` FROM indexers WHERE id = ?`, id))
}

func (s *Store) List() ([]Indexer, error) {
	return s.list(`SELECT ` + cols + ` FROM indexers ORDER BY priority, name`)
}

func (s *Store) ListEnabled() ([]Indexer, error) {
	return s.list(`SELECT ` + cols + ` FROM indexers WHERE enabled = 1 ORDER BY priority, name`)
}

func (s *Store) list(query string) ([]Indexer, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexers := []Indexer{}
	for rows.Next() {
		i, err := scanIndexer(rows)
		if err != nil {
			return nil, err
		}
		indexers = append(indexers, *i)
	}
	return indexers, rows.Err()
}

func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM indexers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
