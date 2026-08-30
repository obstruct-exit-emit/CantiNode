package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Artist is a library artist, matched to a MusicBrainz artist MBID —
// unified across two independent ways CantiNode can come to know about
// one: owning at least one track file (matched during a scan), and/or
// being explicitly monitored (IsMonitored) so its discography is tracked
// for acquisition. Either, neither transiently, or both can be true for
// the same row.
//
// Bio/ImageURL/MetadataFetchedAt are cached from internal/audiodb —
// populated on first monitor or an explicit "refresh metadata" action,
// never fetched just from browsing.
type Artist struct {
	ID                int64      `json:"id"`
	MBID              string     `json:"mbid"`
	Name              string     `json:"name"`
	SortName          string     `json:"sortName"`
	IsMonitored       bool       `json:"isMonitored"`
	LastSyncedAt      *time.Time `json:"lastSyncedAt,omitempty"`
	Bio               string     `json:"bio"`
	ImageURL          string     `json:"imageUrl"`
	MetadataFetchedAt *time.Time `json:"metadataFetchedAt,omitempty"`
	// Genres/Tags/RatingValue/RatingVotes are cached from MusicBrainz
	// (inc=genres+tags+ratings) alongside the discography sync — nothing
	// displays them yet, but they're fetched and stored anyway so a future
	// feature never needs a fresh MusicBrainz round trip for data already
	// available. Genres/Tags stored comma-joined, same convention as
	// ReleaseGroupCache.SecondaryTypes.
	Genres      []string  `json:"genres"`
	Tags        []string  `json:"tags"`
	RatingValue float64   `json:"ratingValue"`
	RatingVotes int       `json:"ratingVotes"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Kind is always "artist" today. Left in place (rather than dropped
	// along with the removed MusicBrainz-series-add feature that used to
	// set it to "series") since it's a real schema column and every
	// existing row already has a value — nothing currently reads it as
	// anything but "artist".
	Kind string `json:"kind"`
}

const artistSelect = `SELECT id, mbid, name, sort_name, is_monitored, last_synced_at, bio, image_url, metadata_fetched_at, genres, tags, rating_value, rating_votes, created_at, updated_at, kind FROM artists`

func scanArtist(row interface{ Scan(...any) error }) (*Artist, error) {
	var a Artist
	var lastSynced, metadataFetchedAt sql.NullTime
	var genres, tags string
	if err := row.Scan(&a.ID, &a.MBID, &a.Name, &a.SortName, &a.IsMonitored, &lastSynced, &a.Bio, &a.ImageURL, &metadataFetchedAt,
		&genres, &tags, &a.RatingValue, &a.RatingVotes, &a.CreatedAt, &a.UpdatedAt, &a.Kind); err != nil {
		return nil, err
	}
	if lastSynced.Valid {
		a.LastSyncedAt = &lastSynced.Time
	}
	if metadataFetchedAt.Valid {
		a.MetadataFetchedAt = &metadataFetchedAt.Time
	}
	a.Genres = splitCommaList(genres)
	a.Tags = splitCommaList(tags)
	return &a, nil
}

// SearchRelevanceName is the wantedArtist value a release search should
// check candidates against (see internal/release.Score's own doc comment
// on artistRelevant).
func (a Artist) SearchRelevanceName() string {
	return a.Name
}

// splitCommaList splits a comma-joined stored value back into a slice,
// always a non-nil []string{} when empty rather than nil, since this ends
// up straight in a JSON response (a nil slice marshals to `null`, not `[]`).
func splitCommaList(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// GetOrCreateArtist returns the existing artist for mbid, inserting a
// minimal one (not monitored, no bio/image yet) if none exists yet.
// Called both as a side effect of matching a track file (scanner) and
// when monitoring an artist with no owned files yet — idempotent either
// way, so whichever happens first just fills in the other's fields later
// rather than creating a duplicate row.
func (s *Store) GetOrCreateArtist(mbid, name, sortName string) (*Artist, error) {
	existing, err := s.getArtistByMBID(mbid)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO artists (mbid, name, sort_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		mbid, name, sortName, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert artist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &Artist{ID: id, MBID: mbid, Name: name, SortName: sortName, Kind: "artist", Genres: []string{}, Tags: []string{}, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) getArtistByMBID(mbid string) (*Artist, error) {
	a, err := scanArtist(s.db.QueryRow(artistSelect+` WHERE mbid = ?`, mbid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artist by mbid: %w", err)
	}
	return a, nil
}

// GetArtist returns a single artist by ID, or ErrNotFound.
func (s *Store) GetArtist(id int64) (*Artist, error) {
	a, err := scanArtist(s.db.QueryRow(artistSelect+` WHERE id = ?`, id))
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
func (s *Store) ListArtists() ([]Artist, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT a.id, a.mbid, a.name, a.sort_name, a.is_monitored, a.last_synced_at, a.bio, a.image_url, a.metadata_fetched_at, a.genres, a.tags, a.rating_value, a.rating_votes, a.created_at, a.updated_at, a.kind
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

// CountOwnedAlbumsByArtist returns, for every artist with at least one, how
// many of its albums have at least one matched track file — the same
// "owned" test ListAlbumsByArtist itself uses, computed for every artist in
// one query rather than one round trip per artist (see
// internal/api's handleListMusicArtists, the library grid). An artist with
// zero owned albums simply has no entry in the returned map — callers
// should treat a missing key as 0, not an error.
func (s *Store) CountOwnedAlbumsByArtist() (map[int64]int, error) {
	rows, err := s.db.Query(`
		SELECT al.artist_id, COUNT(DISTINCT al.id)
		FROM albums al
		JOIN tracks t ON t.album_id = al.id
		JOIN track_files tf ON tf.track_id = t.id
		GROUP BY al.artist_id`)
	if err != nil {
		return nil, fmt.Errorf("count owned albums by artist: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var artistID int64
		var count int
		if err := rows.Scan(&artistID, &count); err != nil {
			return nil, fmt.Errorf("scan owned album count: %w", err)
		}
		out[artistID] = count
	}
	return out, rows.Err()
}

// CountWantedAlbumsByArtist returns, for every artist with at least one,
// how many wanted_albums rows it has — combined with
// CountOwnedAlbumsByArtist, the "total" half of the library grid's
// owned/total subtitle (owned + wanted, deliberately NOT the artist's
// entire discography — an artist with a big catalog and nothing else
// wanted shouldn't read as "1/126", just "1/1"). An artist with nothing
// wanted simply has no entry in the returned map.
func (s *Store) CountWantedAlbumsByArtist() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT artist_id, COUNT(*) FROM wanted_albums GROUP BY artist_id`)
	if err != nil {
		return nil, fmt.Errorf("count wanted albums by artist: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var artistID int64
		var count int
		if err := rows.Scan(&artistID, &count); err != nil {
			return nil, fmt.Errorf("scan wanted album count: %w", err)
		}
		out[artistID] = count
	}
	return out, rows.Err()
}

// CountReleaseGroupsByArtist returns, for every artist with at least one,
// the size of its entire cached discography (artist_release_groups) —
// owned, wanted, and missing combined. Not currently used for the library
// grid's own owned/total subtitle (see CountWantedAlbumsByArtist) — kept
// as its own accurately-named query for whatever wants the real
// discography size rather than just the owned+wanted subset. An artist
// with none cached yet (never monitored, or found by a scan before
// cacheNewArtistsMetadata's background sweep reached it) simply has no
// entry in the returned map.
func (s *Store) CountReleaseGroupsByArtist() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT artist_id, COUNT(*) FROM artist_release_groups GROUP BY artist_id`)
	if err != nil {
		return nil, fmt.Errorf("count release groups by artist: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var artistID int64
		var count int
		if err := rows.Scan(&artistID, &count); err != nil {
			return nil, fmt.Errorf("scan release group count: %w", err)
		}
		out[artistID] = count
	}
	return out, rows.Err()
}

// DeleteArtist deletes id outright — the whole-library "Remove artist"
// action. Cascades (per the schema's own FK setup) to albums -> tracks
// and artist_release_groups. Deliberately does NOT cascade to
// track_files: track_files.track_id is ON DELETE SET NULL, not CASCADE,
// so calling this before every one of the artist's own track_files rows
// has already been deleted or unlinked (track_id cleared back to nil via
// SetTrackFileMatch) would silently orphan them — track_id goes NULL but
// match_status stays whatever it was, e.g. still 'matched' with nothing
// to point at. RemoveArtist is the only intended caller, and it does
// that cleanup first.
func (s *Store) DeleteArtist(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM artists WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete artist %d: %w", id, err)
	}
	return nil
}

// SetArtistMonitored flips id's monitored flag — true when the artist
// starts being tracked for acquisition, false when it's unmonitored.
// Unmonitoring deliberately doesn't touch anything else: owned albums
// and any already-wanted albums are untouched, only the "actively
// tracked" flag changes.
func (s *Store) SetArtistMonitored(id int64, monitored bool) error {
	_, err := s.db.Exec(`UPDATE artists SET is_monitored = ?, updated_at = ? WHERE id = ?`, monitored, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist monitored: %w", err)
	}
	return nil
}

// SetArtistSynced records that id's cached discography (artist_release_groups)
// was just refreshed from MusicBrainz.
func (s *Store) SetArtistSynced(id int64, syncedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE artists SET last_synced_at = ?, updated_at = ? WHERE id = ?`, syncedAt, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist synced: %w", err)
	}
	return nil
}

// SetArtistMetadata records id's fetched bio/image (internal/audiodb) —
// bio and imageURL may both be empty (TheAudioDB simply doesn't have this
// artist), which is still recorded along with fetchedAt so a "refresh
// metadata" click doesn't retry every single time.
func (s *Store) SetArtistMetadata(id int64, bio, imageURL string, fetchedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE artists SET bio = ?, image_url = ?, metadata_fetched_at = ?, updated_at = ? WHERE id = ?`,
		bio, imageURL, fetchedAt, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist metadata: %w", err)
	}
	return nil
}

// SetArtistMusicBrainzMetadata records id's genres/tags/rating, as fetched
// from MusicBrainz alongside its discography sync (see
// internal/api.refreshMusicArtistMetadata) — a separate setter from
// SetArtistMetadata since that one's bio/image come from a different
// provider (TheAudioDB) fetched at a different point in the same flow.
func (s *Store) SetArtistMusicBrainzMetadata(id int64, genres, tags []string, ratingValue float64, ratingVotes int) error {
	_, err := s.db.Exec(
		`UPDATE artists SET genres = ?, tags = ?, rating_value = ?, rating_votes = ?, updated_at = ? WHERE id = ?`,
		strings.Join(genres, ","), strings.Join(tags, ","), ratingValue, ratingVotes, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set artist musicbrainz metadata: %w", err)
	}
	return nil
}

// ReleaseGroupCache is one artist_release_groups row — the cached
// discography backing the unified artist page's Missing section.
// SecondaryTypes is stored comma-joined (see scanReleaseGroupCache)
// since MusicBrainz's own values ("Live", "Compilation", ...) never
// contain a comma themselves.
type ReleaseGroupCache struct {
	ID               int64    `json:"id"`
	ArtistID         int64    `json:"artistId"`
	ReleaseGroupMBID string   `json:"releaseGroupMbid"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primaryType"`
	SecondaryTypes   []string `json:"secondaryTypes"`
	FirstReleaseDate string   `json:"firstReleaseDate"`
}

// ReplaceArtistReleaseGroups replaces artistID's entire cached
// discography with groups — delete-then-reinsert, since the whole point
// of a refresh is "whatever MusicBrainz has now, wholesale" rather than
// a fine-grained diff.
func (s *Store) ReplaceArtistReleaseGroups(artistID int64, groups []ReleaseGroupCache) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM artist_release_groups WHERE artist_id = ?`, artistID); err != nil {
		return fmt.Errorf("clear existing release groups: %w", err)
	}
	for _, g := range groups {
		if _, err := tx.Exec(
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
func (s *Store) ListArtistReleaseGroups(artistID int64) ([]ReleaseGroupCache, error) {
	rows, err := s.db.Query(`
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
// in a JSON response (a nil slice marshals to `null`, not `[]`).
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
// page's Missing section.
func (s *Store) ListMissingArtistReleaseGroups(artistID int64) ([]ReleaseGroupCache, error) {
	rows, err := s.db.Query(`
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

// CalendarEntry is one not-yet-owned release from a monitored artist's
// cached discography, falling inside a calendar query's date window.
type CalendarEntry struct {
	ArtistID         int64    `json:"artistId"`
	ArtistName       string   `json:"artistName"`
	ReleaseGroupMBID string   `json:"releaseGroupMbid"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primaryType"`
	SecondaryTypes   []string `json:"secondaryTypes"`
	FirstReleaseDate string   `json:"firstReleaseDate"`
	// WantedAlbumID is 0 when this release hasn't been marked wanted yet —
	// still worth surfacing on the calendar (a monitored artist's upcoming
	// album), just not yet being searched for.
	WantedAlbumID int64  `json:"wantedAlbumId,omitempty"`
	WantedStatus  string `json:"wantedStatus,omitempty"`
}

// ListUpcomingReleases returns every monitored artist's not-yet-owned
// release groups whose first_release_date falls within [from, to]
// (inclusive, "YYYY-MM-DD" or a MusicBrainz partial date), across the
// whole library — the release Calendar's own query, unlike
// ListMissingArtistReleaseGroups which is scoped to one artist. Ordinary
// lexicographic string comparison works here the same way the rest of this
// package already sorts on first_release_date: MusicBrainz dates are
// always a left-anchored YYYY[-MM[-DD]] prefix, so it agrees with
// chronological order regardless of precision.
func (s *Store) ListUpcomingReleases(from, to string) ([]CalendarEntry, error) {
	rows, err := s.db.Query(`
		SELECT arg.artist_id, a.name, arg.release_group_mbid, arg.title, arg.primary_type, arg.secondary_types, arg.first_release_date,
		       COALESCE(w.id, 0), COALESCE(w.status, '')
		FROM artist_release_groups arg
		JOIN artists a ON a.id = arg.artist_id
		LEFT JOIN wanted_albums w ON w.artist_id = arg.artist_id AND w.release_group_mbid = arg.release_group_mbid
		WHERE a.is_monitored = 1
		  AND arg.first_release_date BETWEEN ? AND ?
		  AND NOT EXISTS (SELECT 1 FROM albums al WHERE al.artist_id = arg.artist_id AND al.release_group_mbid = arg.release_group_mbid)
		ORDER BY arg.first_release_date, a.name, arg.title`, from, to)
	if err != nil {
		return nil, fmt.Errorf("list upcoming releases: %w", err)
	}
	defer rows.Close()

	out := []CalendarEntry{}
	for rows.Next() {
		var e CalendarEntry
		var secondaryTypes string
		if err := rows.Scan(&e.ArtistID, &e.ArtistName, &e.ReleaseGroupMBID, &e.Title, &e.PrimaryType,
			&secondaryTypes, &e.FirstReleaseDate, &e.WantedAlbumID, &e.WantedStatus); err != nil {
			return nil, fmt.Errorf("scan calendar entry: %w", err)
		}
		if secondaryTypes != "" {
			e.SecondaryTypes = strings.Split(secondaryTypes, ",")
		} else {
			e.SecondaryTypes = []string{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
