package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Artist is a library artist, matched to a MusicBrainz artist MBID —
// unified across two independent ways CantiNode can come to know about
// one (see migrations/0004_unified_artist.sql): owning at least one
// track file (matched during a scan), and/or being explicitly monitored
// (IsMonitored) so its discography is tracked for acquisition. Either,
// neither transiently, or both can be true for the same row; there's no
// longer a separate "monitored artist" table.
//
// Bio/ImageURL/MetadataFetchedAt are cached from internal/audiodb —
// populated on first monitor or an explicit "refresh metadata" action
// (see internal/acquisition), never fetched just from browsing.
type Artist struct {
	ID                int64      `json:"id"`
	MBID              string     `json:"mbid"`
	Name              string     `json:"name"`
	SortName          string     `json:"sort_name"`
	IsMonitored       bool       `json:"is_monitored"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	Bio               string     `json:"bio"`
	ImageURL          string     `json:"image_url"`
	MetadataFetchedAt *time.Time `json:"metadata_fetched_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const artistSelect = `SELECT id, mbid, name, sort_name, is_monitored, last_synced_at, bio, image_url, metadata_fetched_at, created_at, updated_at FROM artists`

func scanArtist(row interface{ Scan(...any) error }) (*Artist, error) {
	var a Artist
	var lastSynced, metadataFetchedAt sql.NullTime
	if err := row.Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.IsMonitored, &lastSynced, &a.Bio, &a.ImageURL, &metadataFetchedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if lastSynced.Valid {
		a.LastSyncedAt = &lastSynced.Time
	}
	if metadataFetchedAt.Valid {
		a.MetadataFetchedAt = &metadataFetchedAt.Time
	}
	return &a, nil
}

// GetOrCreateArtist returns the existing artist for mbid, inserting a
// minimal one (not monitored, no bio/image yet) if none exists yet.
// Called both as a side effect of matching a track file (scanner) and by
// internal/acquisition.MonitorArtist for an artist with no owned files
// yet — idempotent either way, so whichever happens first just fills in
// the other's fields later rather than creating a duplicate row.
func (db *DB) GetOrCreateArtist(ctx context.Context, mbid, name, sortName string) (*Artist, error) {
	existing, err := db.getArtistByMBID(ctx, mbid)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO artists (mbid, name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		mbid, name, sortName, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert artist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Artist{ID: id, MBID: mbid, Name: name, SortName: sortName, CreatedAt: now, UpdatedAt: now}, nil
}

func (db *DB) getArtistByMBID(ctx context.Context, mbid string) (*Artist, error) {
	a, err := scanArtist(db.QueryRowContext(ctx, artistSelect+` WHERE mbid = ?`, mbid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artist by mbid: %w", err)
	}
	return a, nil
}

// GetArtist returns a single artist by ID, or ErrNotFound.
func (db *DB) GetArtist(ctx context.Context, id int64) (*Artist, error) {
	a, err := scanArtist(db.QueryRowContext(ctx, artistSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artist: %w", err)
	}
	return a, nil
}

// ListArtists returns every artist CantiNode knows about — either owning
// at least one track file, or explicitly monitored (or both) — ordered
// by sort name. A plain "seen once during a scan but never matched to
// anything" artist can't exist (see GetOrCreateArtist's callers), so this
// deliberately doesn't need a third clause to exclude that case.
func (db *DB) ListArtists(ctx context.Context) ([]Artist, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.mbid, a.name, a.sort_name, a.is_monitored, a.last_synced_at, a.bio, a.image_url, a.metadata_fetched_at, a.created_at, a.updated_at
		FROM artists a
		LEFT JOIN albums al ON al.artist_id = a.id
		LEFT JOIN tracks t ON t.album_id = al.id
		LEFT JOIN track_files tf ON tf.track_id = t.id
		WHERE a.is_monitored = 1 OR tf.id IS NOT NULL
		ORDER BY a.sort_name`)
	if err != nil {
		return nil, fmt.Errorf("list artists: %w", err)
	}
	defer rows.Close()

	// A non-nil empty slice (not "var out []Artist"), so an empty result
	// JSON-encodes to [] rather than null — internal/api returns this
	// straight to the frontend, which does artists.length on it.
	out := []Artist{}
	for rows.Next() {
		a, err := scanArtist(rows)
		if err != nil {
			return nil, fmt.Errorf("scan artist: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// SetArtistMonitored flips id's monitored flag — true when
// acquisition.MonitorArtist starts tracking it, false when it's
// unmonitored. Unmonitoring deliberately doesn't touch anything else:
// owned albums and any already-wanted albums are untouched, only the
// "actively tracked" flag changes (see acquisition.Service.UnmonitorArtist).
func (db *DB) SetArtistMonitored(ctx context.Context, id int64, monitored bool) error {
	_, err := db.ExecContext(ctx, `UPDATE artists SET is_monitored = ?, updated_at = ? WHERE id = ?`, monitored, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist monitored: %w", err)
	}
	return nil
}

// SetArtistSynced records that id's cached discography (artist_release_groups)
// was just refreshed from MusicBrainz.
func (db *DB) SetArtistSynced(ctx context.Context, id int64, syncedAt time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE artists SET last_synced_at = ?, updated_at = ? WHERE id = ?`, syncedAt, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist synced: %w", err)
	}
	return nil
}

// SetArtistMetadata records id's fetched bio/image (internal/audiodb) —
// bio and imageURL may both be empty (TheAudioDB simply doesn't have this
// artist), which is still recorded along with fetchedAt so a "refresh
// metadata" click doesn't retry every single time.
func (db *DB) SetArtistMetadata(ctx context.Context, id int64, bio, imageURL string, fetchedAt time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE artists SET bio = ?, image_url = ?, metadata_fetched_at = ?, updated_at = ? WHERE id = ?`,
		bio, imageURL, fetchedAt, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist metadata: %w", err)
	}
	return nil
}

// ReleaseGroupCache is one artist_release_groups row — the cached
// discography backing the unified artist page's "Missing" section (see
// migrations/0004_unified_artist.sql). SecondaryTypes is stored
// comma-joined (see releaseGroupCacheSelect's scan) since MusicBrainz's
// own values ("Live", "Compilation", ...) never contain a comma
// themselves.
type ReleaseGroupCache struct {
	ID               int64    `json:"id"`
	ArtistID         int64    `json:"artist_id"`
	ReleaseGroupMBID string   `json:"release_group_mbid"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary_type"`
	SecondaryTypes   []string `json:"secondary_types"`
	FirstReleaseDate string   `json:"first_release_date"`
}

// ReplaceArtistReleaseGroups replaces artistID's entire cached
// discography with groups — delete-then-reinsert, matching this
// package's other simple bulk-replace idioms, since the whole point of a
// refresh is "whatever MusicBrainz has now, wholesale" rather than a
// fine-grained diff.
func (db *DB) ReplaceArtistReleaseGroups(ctx context.Context, artistID int64, groups []ReleaseGroupCache) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_release_groups WHERE artist_id = ?`, artistID); err != nil {
		return fmt.Errorf("clear existing release groups: %w", err)
	}
	for _, g := range groups {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO artist_release_groups (artist_id, release_group_mbid, title, primary_type, secondary_types, first_release_date)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			artistID, g.ReleaseGroupMBID, g.Title, g.PrimaryType, strings.Join(g.SecondaryTypes, ","), g.FirstReleaseDate); err != nil {
			return fmt.Errorf("insert release group %s: %w", g.ReleaseGroupMBID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ListArtistReleaseGroups returns artistID's entire cached discography,
// most recent release first.
func (db *DB) ListArtistReleaseGroups(ctx context.Context, artistID int64) ([]ReleaseGroupCache, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, artist_id, release_group_mbid, title, primary_type, secondary_types, first_release_date
		FROM artist_release_groups
		WHERE artist_id = ?
		ORDER BY first_release_date DESC, title`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list artist release groups: %w", err)
	}
	defer rows.Close()

	out := []ReleaseGroupCache{}
	for rows.Next() {
		g, err := scanReleaseGroupCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// scanReleaseGroupCache scans one artist_release_groups row, splitting
// its comma-joined secondary_types back into a slice — always a non-nil
// []string{} when there are none, not nil, since this ends up straight
// in a JSON response (a nil slice marshals to `null`, not `[]`, which
// crashed the web UI's own `.includes()` check on it).
func scanReleaseGroupCache(row interface{ Scan(...any) error }) (ReleaseGroupCache, error) {
	var g ReleaseGroupCache
	var secondaryTypes string
	if err := row.Scan(&g.ID, &g.ArtistID, &g.ReleaseGroupMBID, &g.Title, &g.PrimaryType, &secondaryTypes, &g.FirstReleaseDate); err != nil {
		return g, fmt.Errorf("scan release group: %w", err)
	}
	if secondaryTypes != "" {
		g.SecondaryTypes = strings.Split(secondaryTypes, ",")
	} else {
		g.SecondaryTypes = []string{}
	}
	return g, nil
}

// ListMissingArtistReleaseGroups returns artistID's cached discography
// minus whatever's already an owned album (by release_group_mbid) or
// already a wanted album for this artist — backs the unified artist
// page's "Missing" section (GET /api/v1/artists/{id}/missing). Not part
// of ListArtistReleaseGroups itself since most callers of that one (e.g.
// a future "full discography" view) want the unfiltered list.
func (db *DB) ListMissingArtistReleaseGroups(ctx context.Context, artistID int64) ([]ReleaseGroupCache, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT arg.id, arg.artist_id, arg.release_group_mbid, arg.title, arg.primary_type, arg.secondary_types, arg.first_release_date
		FROM artist_release_groups arg
		WHERE arg.artist_id = ?
		  AND NOT EXISTS (SELECT 1 FROM albums al WHERE al.artist_id = arg.artist_id AND al.release_group_mbid = arg.release_group_mbid)
		  AND NOT EXISTS (SELECT 1 FROM wanted_albums w WHERE w.artist_id = arg.artist_id AND w.release_group_mbid = arg.release_group_mbid)
		ORDER BY arg.first_release_date DESC, arg.title`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list missing artist release groups: %w", err)
	}
	defer rows.Close()

	out := []ReleaseGroupCache{}
	for rows.Next() {
		g, err := scanReleaseGroupCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
