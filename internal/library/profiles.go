package library

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// QualityProfile holds per-media-type release rules. Formats is ordered,
// best first; only listed formats are grabbable. One profile per media type
// is the default used by searches (per-author assignment can layer on later).
type QualityProfile struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	MediaType   string   `json:"mediaType"`
	Formats     []string `json:"formats"`
	Language    string   `json:"language"` // "" accepts any language
	RetailBonus int      `json:"retailBonus"`
	MinSize     int64    `json:"minSize"`
	MaxSize     int64    `json:"maxSize"`
	// UpgradesAllowed keeps books wanted while their owned format is below
	// Cutoff (empty cutoff = the profile's best format).
	UpgradesAllowed bool   `json:"upgradesAllowed"`
	Cutoff          string `json:"cutoff,omitempty"`
	IsDefault       bool   `json:"isDefault"`
	AddedAt         string `json:"addedAt"`
}

// formatsByMediaType lists the grabbable formats per media type.
var formatsByMediaType = map[string][]string{
	"ebook":     {"epub", "azw3", "mobi", "pdf"},
	"audiobook": {"m4b", "m4a", "mp3", "flac", "opus"},
	"manga":     {"cbz", "cbr", "epub"},
	"comic":     {"cbz", "cbr", "pdf"},
	"magazine":  {"pdf", "epub", "cbz"},
}

// ValidateProfile normalizes and checks a profile definition in place.
func ValidateProfile(p *QualityProfile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("name is required")
	}
	if p.MediaType == "" {
		p.MediaType = "ebook"
	}
	known, ok := formatsByMediaType[p.MediaType]
	if !ok {
		return fmt.Errorf("unknown media type %q", p.MediaType)
	}

	seen := map[string]bool{}
	normalized := make([]string, 0, len(p.Formats))
	for _, f := range p.Formats {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		valid := false
		for _, k := range known {
			if f == k {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("format %q is not valid for %s", f, p.MediaType)
		}
		seen[f] = true
		normalized = append(normalized, f)
	}
	if len(normalized) == 0 {
		return errors.New("at least one format is required")
	}
	p.Formats = normalized

	p.Language = strings.ToLower(strings.TrimSpace(p.Language))
	if p.RetailBonus < 0 || p.RetailBonus > 100 {
		return errors.New("retailBonus must be 0-100")
	}
	p.Cutoff = strings.ToLower(strings.TrimSpace(p.Cutoff))
	if p.Cutoff != "" {
		found := false
		for _, f := range p.Formats {
			if f == p.Cutoff {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("cutoff %q is not one of the profile's formats", p.Cutoff)
		}
	}
	// Unset size bounds get the standard sanity window rather than
	// silently disabling the size checks.
	if p.MinSize == 0 && p.MaxSize == 0 {
		p.MinSize = 20 << 10
		p.MaxSize = 500 << 20
	}
	if p.MinSize < 0 || (p.MaxSize > 0 && p.MaxSize <= p.MinSize) {
		return errors.New("size bounds are inverted")
	}
	return nil
}

const profileCols = `id, name, media_type, formats, language, retail_bonus, min_size, max_size, upgrades_allowed, cutoff, is_default, added_at`

func scanProfile(row interface{ Scan(...any) error }) (*QualityProfile, error) {
	var p QualityProfile
	var formats string
	err := row.Scan(&p.ID, &p.Name, &p.MediaType, &formats, &p.Language,
		&p.RetailBonus, &p.MinSize, &p.MaxSize, &p.UpgradesAllowed, &p.Cutoff, &p.IsDefault, &p.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if formats != "" {
		p.Formats = strings.Split(formats, ",")
	}
	return &p, nil
}

func (s *Store) AddProfile(p *QualityProfile) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	return s.db.QueryRow(`
		INSERT INTO quality_profiles (name, media_type, formats, language, retail_bonus, min_size, max_size, upgrades_allowed, cutoff, is_default)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, added_at`,
		p.Name, p.MediaType, strings.Join(p.Formats, ","), p.Language,
		p.RetailBonus, p.MinSize, p.MaxSize, p.UpgradesAllowed, p.Cutoff, p.IsDefault,
	).Scan(&p.ID, &p.AddedAt)
}

func (s *Store) UpdateProfile(p *QualityProfile) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	res, err := s.db.Exec(`
		UPDATE quality_profiles
		SET name = ?, formats = ?, language = ?, retail_bonus = ?, min_size = ?, max_size = ?, upgrades_allowed = ?, cutoff = ?
		WHERE id = ?`,
		p.Name, strings.Join(p.Formats, ","), p.Language,
		p.RetailBonus, p.MinSize, p.MaxSize, p.UpgradesAllowed, p.Cutoff, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetProfile(id int64) (*QualityProfile, error) {
	return scanProfile(s.db.QueryRow(`SELECT `+profileCols+` FROM quality_profiles WHERE id = ?`, id))
}

func (s *Store) ListProfiles() ([]QualityProfile, error) {
	rows, err := s.db.Query(`SELECT ` + profileCols + ` FROM quality_profiles ORDER BY media_type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := []QualityProfile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, *p)
	}
	return profiles, rows.Err()
}

// DefaultProfile returns the media type's default profile.
func (s *Store) DefaultProfile(mediaType string) (*QualityProfile, error) {
	return scanProfile(s.db.QueryRow(
		`SELECT `+profileCols+` FROM quality_profiles WHERE media_type = ? AND is_default = 1 LIMIT 1`,
		mediaType))
}

// SetDefaultProfile makes the profile its media type's default (clearing the
// previous one).
func (s *Store) SetDefaultProfile(id int64) error {
	p, err := s.GetProfile(id)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE quality_profiles SET is_default = 0 WHERE media_type = ?`, p.MediaType); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE quality_profiles SET is_default = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProfile removes a profile; the default cannot be deleted (pick a new
// default first) so searches never lose their rules.
func (s *Store) DeleteProfile(id int64) error {
	p, err := s.GetProfile(id)
	if err != nil {
		return err
	}
	if p.IsDefault {
		return errors.New("cannot delete the default profile — set another default first")
	}
	_, err = s.db.Exec(`DELETE FROM quality_profiles WHERE id = ?`, id)
	return err
}
