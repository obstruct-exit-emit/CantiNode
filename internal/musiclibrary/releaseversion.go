package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	// Fetched is false only for a placeholder row migration 022 carried
	// over from the old single-release-tracklist scheme (release_mbid/
	// title only, everything else blank) — every row ReplaceReleaseGroupVersions
	// itself ever inserts is genuinely fetched, so this is always true for
	// anything written after that one-time migration. Replaced an earlier
	// heuristic (guessing "placeholder" from TrackCount==0 && Status=="")
	// that could misclassify a real MusicBrainz release with neither field
	// populated — see migration 023.
	Fetched   bool      `json:"fetched"`
	FetchedAt time.Time `json:"fetchedAt"`
}

const releaseGroupVersionSelect = `SELECT id, release_group_mbid, release_mbid, title, release_date, country, status, disambiguation, track_count, media_summary, is_representative, fetched, fetched_at FROM release_group_versions`

func scanReleaseGroupVersion(row interface{ Scan(...any) error }) (ReleaseGroupVersion, error) {
	var v ReleaseGroupVersion
	err := row.Scan(&v.ID, &v.ReleaseGroupMBID, &v.ReleaseMBID, &v.Title, &v.ReleaseDate, &v.Country,
		&v.Status, &v.Disambiguation, &v.TrackCount, &v.MediaSummary, &v.IsRepresentative, &v.Fetched, &v.FetchedAt)
	return v, err
}

// ReplaceReleaseGroupVersions replaces every cached version of
// releaseGroupMBID with versions — delete-then-reinsert, mirroring
// ReplaceArtistReleaseGroups: the point of a refresh is "whatever
// MusicBrainz has now, wholesale," not a fine-grained diff. Every row this
// writes is marked fetched=1 unconditionally — this function is only ever
// called with data just pulled live from MusicBrainz (see internal/api's
// cacheReleaseGroupVersions), never with a placeholder.
func (s *Store) ReplaceReleaseGroupVersions(releaseGroupMBID string, versions []ReleaseGroupVersion) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM release_group_versions WHERE release_group_mbid = ?`, releaseGroupMBID); err != nil {
		return fmt.Errorf("clear existing versions: %w", err)
	}
	if len(versions) > 0 {
		rowPlaceholder := "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)"
		valuePlaceholders := make([]string, len(versions))
		args := make([]any, 0, len(versions)*10)
		for i, v := range versions {
			valuePlaceholders[i] = rowPlaceholder
			args = append(args, releaseGroupMBID, v.ReleaseMBID, v.Title, v.ReleaseDate, v.Country, v.Status,
				v.Disambiguation, v.TrackCount, v.MediaSummary, v.IsRepresentative)
		}
		if _, err := tx.Exec(
			`INSERT INTO release_group_versions
			 (release_group_mbid, release_mbid, title, release_date, country, status, disambiguation, track_count, media_summary, is_representative, fetched)
			 VALUES `+strings.Join(valuePlaceholders, ","),
			args...); err != nil {
			return fmt.Errorf("insert versions: %w", err)
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

// ListReleaseGroupVersionsBulk is the bulk form of ListReleaseGroupVersions
// — one query for every release group in releaseGroupMBIDs instead of one
// per release group. Used by internal/api's purgeArtistCaches to collect
// every cached release MBID (for cover-art purging) across an entire
// artist's unreferenced discography at once. Grouping order within each
// release group's slice is unspecified (unlike ListReleaseGroupVersions,
// which orders by representative/date) — every caller of this bulk form so
// far only needs the release MBIDs, not a meaningful ordering.
func (s *Store) ListReleaseGroupVersionsBulk(releaseGroupMBIDs []string) (map[string][]ReleaseGroupVersion, error) {
	out := make(map[string][]ReleaseGroupVersion, len(releaseGroupMBIDs))
	if len(releaseGroupMBIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(releaseGroupMBIDs))
	args := make([]any, len(releaseGroupMBIDs))
	for i, mbid := range releaseGroupMBIDs {
		placeholders[i] = "?"
		args[i] = mbid
	}
	rows, err := s.db.Query(releaseGroupVersionSelect+
		` WHERE release_group_mbid IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("list release group versions in bulk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanReleaseGroupVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan release group version: %w", err)
		}
		out[v.ReleaseGroupMBID] = append(out[v.ReleaseGroupMBID], v)
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
		`SELECT COUNT(1) FROM release_group_versions WHERE release_group_mbid = ? AND fetched = 1`,
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

// GetReleaseGroupVersionByRelease returns the specific cached version
// matching releaseMBID exactly — the edition actually owned/matched, as
// opposed to GetRepresentativeReleaseVersion's "whichever one MusicBrainz
// or CantiNode considers canonical." ErrNotFound covers both "this
// release group's versions were never cached" and "they were, but this
// particular release isn't among them" — internal/musicscanner's WriteTags
// treats both the same way (best-effort: leave the country/status/media
// tags blank rather than fail the whole write).
func (s *Store) GetReleaseGroupVersionByRelease(releaseGroupMBID, releaseMBID string) (*ReleaseGroupVersion, error) {
	v, err := scanReleaseGroupVersion(s.db.QueryRow(
		releaseGroupVersionSelect+` WHERE release_group_mbid = ? AND release_mbid = ?`,
		releaseGroupMBID, releaseMBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get release group version by release: %w", err)
	}
	return &v, nil
}

// ReleaseGroupMBIDsStillReferenced filters releaseGroupMBIDs down to the
// subset that still have an artist_release_groups row for ANY artist — used
// before purging shared caches on artist removal (see internal/api's
// purgeArtistCaches): the same release_group_mbid can legitimately be cached
// under more than one artist (e.g. a collaboration/various-artists release
// group each artist's own discography sync pulled in independently;
// artist_release_groups is only unique per (artist_id, release_group_mbid),
// not per release_group_mbid alone), so a release group still referenced by
// a still-monitored artist must not have its cached
// version/tracklist/cover-art metadata wiped just because a different artist
// that also referenced it was removed.
func (s *Store) ReleaseGroupMBIDsStillReferenced(releaseGroupMBIDs []string) (map[string]bool, error) {
	referenced := make(map[string]bool, len(releaseGroupMBIDs))
	if len(releaseGroupMBIDs) == 0 {
		return referenced, nil
	}
	placeholders := make([]string, len(releaseGroupMBIDs))
	args := make([]any, len(releaseGroupMBIDs))
	for i, mbid := range releaseGroupMBIDs {
		placeholders[i] = "?"
		args[i] = mbid
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT release_group_mbid FROM artist_release_groups WHERE release_group_mbid IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("check release group references: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mbid string
		if err := rows.Scan(&mbid); err != nil {
			return nil, fmt.Errorf("scan release group reference: %w", err)
		}
		referenced[mbid] = true
	}
	return referenced, rows.Err()
}

// ReleaseGroupMBIDsWithRealVersions is the bulk form of
// HasReleaseGroupVersions — filters releaseGroupMBIDs down to the subset
// that already have at least one genuinely-fetched version cached (not
// just a migration-022 placeholder row), in one query instead of one per
// release group. Used by the backfill sweep (see internal/api's
// backfillReleaseGroupVersions) to check an artist's entire discography
// at once rather than round-tripping per release group.
func (s *Store) ReleaseGroupMBIDsWithRealVersions(releaseGroupMBIDs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(releaseGroupMBIDs))
	if len(releaseGroupMBIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(releaseGroupMBIDs))
	args := make([]any, len(releaseGroupMBIDs))
	for i, mbid := range releaseGroupMBIDs {
		placeholders[i] = "?"
		args[i] = mbid
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT release_group_mbid FROM release_group_versions
		 WHERE fetched = 1 AND release_group_mbid IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("check release group versions in bulk: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mbid string
		if err := rows.Scan(&mbid); err != nil {
			return nil, fmt.Errorf("scan release group mbid: %w", err)
		}
		out[mbid] = true
	}
	return out, rows.Err()
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
	placeholders := make([]string, len(releaseGroupMBIDs))
	args := make([]any, len(releaseGroupMBIDs))
	for i, mbid := range releaseGroupMBIDs {
		placeholders[i] = "?"
		args[i] = mbid
	}
	inClause := "(" + strings.Join(placeholders, ",") + ")"

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM release_group_versions WHERE release_group_mbid IN `+inClause, args...); err != nil {
		return fmt.Errorf("delete release group versions: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM release_tracklist_cache WHERE release_group_mbid IN `+inClause, args...); err != nil {
		return fmt.Errorf("delete release tracklist cache: %w", err)
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
