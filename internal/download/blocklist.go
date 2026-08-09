package download

import (
	"strings"
)

// BlockEntry is one release that failed to download and must never be
// grabbed again. Matching is by release guid when known, with the
// normalized title as a fallback (qBittorrent grabs have no guid until
// the failure is matched back to the grab record).
type BlockEntry struct {
	ID        int64  `json:"id"`
	GUID      string `json:"guid,omitempty"`
	Title     string `json:"title"`
	Reason    string `json:"reason,omitempty"`
	BlockedAt string `json:"blockedAt"`
}

// AddBlock records a failed release. Duplicate entries (same guid or title
// already present) are quietly skipped.
func (s *Store) AddBlock(guid, title, reason string) error {
	blocked, err := s.BlockedKeys()
	if err != nil {
		return err
	}
	if (guid != "" && blocked[guid]) || blocked[normalizeBlockKey(title)] {
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO blocklist (guid, title, reason) VALUES (?, ?, ?)`,
		guid, title, reason)
	return err
}

// ListBlocklist returns blocked releases, newest first.
func (s *Store) ListBlocklist() ([]BlockEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, guid, title, reason, blocked_at FROM blocklist
		ORDER BY blocked_at DESC, id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []BlockEntry{}
	for rows.Next() {
		var e BlockEntry
		if err := rows.Scan(&e.ID, &e.GUID, &e.Title, &e.Reason, &e.BlockedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteBlock removes one blocklist entry (un-blocking the release).
func (s *Store) DeleteBlock(id int64) error {
	res, err := s.db.Exec(`DELETE FROM blocklist WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BlockedKeys returns a lookup set for release filtering: guids plus
// normalized titles of every blocked release.
func (s *Store) BlockedKeys() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT guid, title FROM blocklist`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := map[string]bool{}
	for rows.Next() {
		var guid, title string
		if err := rows.Scan(&guid, &title); err != nil {
			return nil, err
		}
		if guid != "" {
			keys[guid] = true
		}
		keys[normalizeBlockKey(title)] = true
	}
	return keys, rows.Err()
}

// IsBlocked reports whether a release (by guid or title) is blocklisted.
func IsBlocked(blocked map[string]bool, guid, title string) bool {
	if guid != "" && blocked[guid] {
		return true
	}
	return blocked[normalizeBlockKey(title)]
}

// normalizeBlockKey lowercases and collapses whitespace so title matching
// survives cosmetic differences between the grab and the re-offered release.
func normalizeBlockKey(title string) string {
	return "t:" + strings.Join(strings.Fields(strings.ToLower(title)), " ")
}
