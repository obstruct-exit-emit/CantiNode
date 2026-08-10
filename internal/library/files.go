package library

// RootFolder mirrors the root_folders table (managed by the rootfolder API);
// generic across every media type, including music — musicscanner and
// internal/health both read it via this same table.
type RootFolder struct {
	ID        int64  `json:"id"`
	MediaType string `json:"mediaType"`
	Path      string `json:"path"`
}

func (s *Store) ListRootFolders() ([]RootFolder, error) {
	rows, err := s.db.Query(`SELECT id, media_type, path FROM root_folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	folders := []RootFolder{}
	for rows.Next() {
		var f RootFolder
		if err := rows.Scan(&f.ID, &f.MediaType, &f.Path); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}
