package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CachedTracklist is a release group's tracklist preview, cached once
// after its first live MusicBrainz fetch — see internal/api's
// handleGetReleaseGroupTracklist. TracksJSON is stored pre-serialized
// (opaque to this package) rather than decoded back into a Go slice, since
// the API handler is the only reader and already needs it as JSON for the
// response body.
type CachedTracklist struct {
	ReleaseGroupMBID string
	ReleaseMBID      string
	ReleaseTitle     string
	TracksJSON       string
	FetchedAt        time.Time
}

// GetCachedTracklist returns releaseGroupMBID's cached tracklist, or
// ErrNotFound if it hasn't been fetched yet.
func (s *Store) GetCachedTracklist(releaseGroupMBID string) (*CachedTracklist, error) {
	var c CachedTracklist
	err := s.db.QueryRow(
		`SELECT release_group_mbid, release_mbid, release_title, tracks_json, fetched_at
		 FROM release_group_tracklist_cache WHERE release_group_mbid = ?`, releaseGroupMBID,
	).Scan(&c.ReleaseGroupMBID, &c.ReleaseMBID, &c.ReleaseTitle, &c.TracksJSON, &c.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get cached tracklist: %w", err)
	}
	return &c, nil
}

// SetCachedTracklist stores (or overwrites) releaseGroupMBID's tracklist —
// idempotent, so a duplicate fetch from two concurrent requests just
// overwrites with the same data rather than erroring.
func (s *Store) SetCachedTracklist(releaseGroupMBID, releaseMBID, releaseTitle, tracksJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO release_group_tracklist_cache (release_group_mbid, release_mbid, release_title, tracks_json, fetched_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(release_group_mbid) DO UPDATE SET
		   release_mbid = excluded.release_mbid,
		   release_title = excluded.release_title,
		   tracks_json = excluded.tracks_json,
		   fetched_at = excluded.fetched_at`,
		releaseGroupMBID, releaseMBID, releaseTitle, tracksJSON, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("set cached tracklist: %w", err)
	}
	return nil
}
