package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ReleaseGroupVersion is one known release (a specific pressing/edition)
// of a release group — see migration 022. Cached so the matching UI can
// offer a version picker (and a folder's file count can be scored against
// each candidate's own TrackCount) without ever calling MusicBrainz again
// once an artist's discography has been synced.
type ReleaseGroupVersion struct {
	ID               int64     `json:"id"`
	ReleaseGroupMBID string    `json:"releaseGroupMbid"`
	ReleaseMBID      string    `json:"releaseMbid"`
	Title            string    `json:"title"`
	ReleaseDate      string    `json:"releaseDate"`
	Country          string    `json:"country"`
	Status           string    `json:"status"`
	Disambiguation   string    `json:"disambiguation"`
	TrackCount       int       `json:"trackCount"`
	MediaSummary     string    `json:"mediaSummary"`
	IsRepresentative bool      `json:"isRepresentative"`
	FetchedAt        time.Time `json:"fetchedAt"`
}

const releaseGroupVersionSelect = `SELECT id, release_group_mbid, release_mbid, title, release_date, country, status, disambiguation, track_count, media_summary, is_representative, fetched_at FROM release_group_versions`

func scanReleaseGroupVersion(row interface{ Scan(...any) error }) (ReleaseGroupVersion, error) {
	var v ReleaseGroupVersion
	err := row.Scan(&v.ID, &v.ReleaseGroupMBID, &v.ReleaseMBID, &v.Title, &v.ReleaseDate, &v.Country,
		&v.Status, &v.Disambiguation, &v.TrackCount, &v.MediaSummary, &v.IsRepresentative, &v.FetchedAt)
	return v, err
}

// ReplaceReleaseGroupVersions replaces every cached version of
// releaseGroupMBID with versions — delete-then-reinsert, mirroring
// ReplaceArtistReleaseGroups: the point of a refresh is "whatever
// MusicBrainz has now, wholesale," not a fine-grained diff.
func (s *Store) ReplaceReleaseGroupVersions(releaseGroupMBID string, versions []ReleaseGroupVersion) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM release_group_versions WHERE release_group_mbid = ?`, releaseGroupMBID); err != nil {
		return fmt.Errorf("clear existing versions: %w", err)
	}
	for _, v := range versions {
		if _, err := tx.Exec(
			`INSERT INTO release_group_versions
			 (release_group_mbid, release_mbid, title, release_date, country, status, disambiguation, track_count, media_summary, is_representative)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			releaseGroupMBID, v.ReleaseMBID, v.Title, v.ReleaseDate, v.Country, v.Status,
			v.Disambiguation, v.TrackCount, v.MediaSummary, v.IsRepresentative); err != nil {
			return fmt.Errorf("insert version %s: %w", v.ReleaseMBID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListReleaseGroupVersions returns every cached version of
// releaseGroupMBID, the representative version first, then newest release
// date first.
func (s *Store) ListReleaseGroupVersions(releaseGroupMBID string) ([]ReleaseGroupVersion, error) {
	rows, err := s.db.Query(releaseGroupVersionSelect+`
		WHERE release_group_mbid = ?
		ORDER BY is_representative DESC, release_date DESC, title`, releaseGroupMBID)
	if err != nil {
		return nil, fmt.Errorf("list release group versions: %w", err)
	}
	defer rows.Close()

	out := []ReleaseGroupVersion{}
	for rows.Next() {
		v, err := scanReleaseGroupVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan release group version: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// HasReleaseGroupVersions reports whether releaseGroupMBID has a genuinely
// fetched cached version — not just the placeholder row migration 022
// carried over from the old single-release-tracklist scheme (release_mbid/
// title only, every other column left at its blank default). A migrated
// placeholder must count as "not yet cached" here, or an artist that
// predates this feature would never get backfilled: HasReleaseGroupVersions
// would report true for its very first (blank) row and
// backfillReleaseGroupVersions would skip it forever. Used to find artists
// that predate release-version caching (see internal/api's
// backfillReleaseGroupVersions) and by handleListReleaseGroupVersions's own
// cache-miss fallback.
func (s *Store) HasReleaseGroupVersions(releaseGroupMBID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM release_group_versions WHERE release_group_mbid = ? AND (track_count > 0 OR status != '')`,
		releaseGroupMBID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check release group versions: %w", err)
	}
	return n > 0, nil
}

// GetRepresentativeReleaseVersion returns releaseGroupMBID's representative
// version (see internal/api's pickRepresentativeRelease — an Official
// release, earliest-dated tie-break), or ErrNotFound if nothing's cached
// yet for this release group at all.
func (s *Store) GetRepresentativeReleaseVersion(releaseGroupMBID string) (*ReleaseGroupVersion, error) {
	v, err := scanReleaseGroupVersion(s.db.QueryRow(
		releaseGroupVersionSelect+` WHERE release_group_mbid = ? ORDER BY is_representative DESC, release_date DESC LIMIT 1`,
		releaseGroupMBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get representative release version: %w", err)
	}
	return &v, nil
}

// DeleteReleaseGroupCache purges every cached version and tracklist for
// releaseGroupMBIDs — used when an artist is removed (see internal/api's
// handleRemoveMusicArtist): unlike artist_release_groups (which cascades
// away via its FK to artists), these two tables are keyed globally by
// release-group/release MBID with no FK back to any one artist, so nothing
// deletes them automatically — an artist's cached discography metadata
// would otherwise outlive the artist itself.
func (s *Store) DeleteReleaseGroupCache(releaseGroupMBIDs []string) error {
	if len(releaseGroupMBIDs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, mbid := range releaseGroupMBIDs {
		if _, err := tx.Exec(`DELETE FROM release_group_versions WHERE release_group_mbid = ?`, mbid); err != nil {
			return fmt.Errorf("delete release group versions %s: %w", mbid, err)
		}
		if _, err := tx.Exec(`DELETE FROM release_tracklist_cache WHERE release_group_mbid = ?`, mbid); err != nil {
			return fmt.Errorf("delete release tracklist cache %s: %w", mbid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// CachedTracklist is one release's full tracklist, cached once after its
// first live MusicBrainz fetch. TracksJSON is stored pre-serialized
// (opaque to this package, a marshaled musicbrainz.ReleaseWithTracklist)
// rather than decoded back into a Go struct, since musiclibrary doesn't
// depend on the musicbrainz package — internal/api is the only reader and
// already needs it as JSON either to decode or to re-serve directly.
type CachedTracklist struct {
	ReleaseMBID      string
	ReleaseGroupMBID string
	TracksJSON       string
	FetchedAt        time.Time
}

// GetCachedTracklist returns releaseMBID's cached tracklist, or
// ErrNotFound if it hasn't been fetched yet.
func (s *Store) GetCachedTracklist(releaseMBID string) (*CachedTracklist, error) {
	var c CachedTracklist
	err := s.db.QueryRow(
		`SELECT release_mbid, release_group_mbid, tracks_json, fetched_at
		 FROM release_tracklist_cache WHERE release_mbid = ?`, releaseMBID,
	).Scan(&c.ReleaseMBID, &c.ReleaseGroupMBID, &c.TracksJSON, &c.FetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get cached tracklist: %w", err)
	}
	return &c, nil
}

// SetCachedTracklist stores (or overwrites) releaseMBID's tracklist —
// idempotent, so a duplicate fetch from two concurrent requests just
// overwrites with the same data rather than erroring.
func (s *Store) SetCachedTracklist(releaseMBID, releaseGroupMBID, tracksJSON string) error {
	_, err := s.db.Exec(
		`INSERT INTO release_tracklist_cache (release_mbid, release_group_mbid, tracks_json, fetched_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(release_mbid) DO UPDATE SET
		   release_group_mbid = excluded.release_group_mbid,
		   tracks_json = excluded.tracks_json,
		   fetched_at = excluded.fetched_at`,
		releaseMBID, releaseGroupMBID, tracksJSON, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("set cached tracklist: %w", err)
	}
	return nil
}
