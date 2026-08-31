package musiclibrary

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Album is a library album (MusicBrainz release), matched to a specific
// release MBID under ArtistID.
//
// Description/Mood/DescriptionFetchedAt are cached from internal/audiodb —
// TheAudioDB, the only provider CantiNode has either from (MusicBrainz's
// own release/release-group data carries neither field) — fetched once,
// lazily, the first time the album detail page is actually viewed (see
// internal/api's handleGetMusicAlbumDescription), same
// cache-forever-after-one-try convention as Artist.Bio/MetadataFetchedAt:
// DescriptionFetchedAt distinguishes "never tried" (nil) from "tried,
// TheAudioDB had nothing" (non-nil, Description/Mood still ""). Mood
// shares Description's own fetched-at flag rather than getting a second
// one — both come from the exact same TheAudioDB response.
type Album struct {
	ID                   int64      `json:"id"`
	ArtistID             int64      `json:"artistId"`
	MBID                 string     `json:"mbid"`
	ReleaseGroupMBID     string     `json:"releaseGroupMbid"`
	Title                string     `json:"title"`
	ReleaseDate          string     `json:"releaseDate"`
	PrimaryType          string     `json:"primaryType"`
	Description          string     `json:"description"`
	Mood                 string     `json:"mood"`
	DescriptionFetchedAt *time.Time `json:"descriptionFetchedAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	// SecondaryTypes ("Live", "Compilation", ...) is never populated by
	// this package's own scan — an owned album row only ever stores its
	// primary type. internal/api's list-by-artist handler fills this in
	// from the artist's cached discography (ArtistReleaseGroup.SecondaryTypes,
	// matched by ReleaseGroupMBID) for callers that need the finer
	// category (the Albums grid's own "Type" sort — see
	// web/src/components/SortControl.tsx's releaseCategory). nil for any
	// other caller.
	SecondaryTypes []string `json:"secondaryTypes,omitempty"`
}

// GetOrCreateAlbum returns the existing album for artistID+releaseGroupMBID,
// inserting one if none exists yet. Album identity is the release GROUP —
// MusicBrainz's canonical album — not the specific release named by mbid:
// a Recording independently resolves to whichever of its own releases
// musicbrainz.Recording.BestRelease picks, so two tracks belonging to the
// very same physical album can easily carry two different release mbids.
// Deduplicating on release_group_mbid instead means they still collapse
// into one album row; whichever release's mbid/title/release_date/
// primary_type got recorded first is kept as-is on later calls, same as
// this package's other GetOrCreate* functions.
//
// releaseGroupMBID should never actually be empty (MusicBrainz's own
// release.ReleaseGroup.ID always is set), but falls back to the old
// mbid-keyed lookup defensively rather than risk duplicate rows if it
// ever is.
//
// mbid itself also carries its own, separate database-wide UNIQUE
// constraint (a specific physical release can only ever belong to one
// album row, however it's reached) — found live: two tracks of the very
// same logical release-group can independently resolve, via
// Recording.BestRelease, to two different specific release editions (or,
// rarer, briefly to a different filing artist before a per-track
// correction applies) whose mbid one of them already claims. A plain
// check-then-insert can't see that until the insert itself fails, so the
// insert goes through ON CONFLICT(mbid) DO NOTHING — atomic, no separate
// race window — and the row is always read back by mbid afterward,
// whether this call just inserted it or another one already had.
func (s *Store) GetOrCreateAlbum(artistID int64, mbid, releaseGroupMBID, title, releaseDate, primaryType string) (*Album, error) {
	if releaseGroupMBID != "" {
		existing, err := s.getAlbumByReleaseGroupMBID(artistID, releaseGroupMBID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}

	now := time.Now().UTC()
	if _, err := s.db.Exec(
		`INSERT INTO albums (artist_id, mbid, release_group_mbid, title, release_date, primary_type, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(mbid) DO NOTHING`,
		artistID, mbid, releaseGroupMBID, title, releaseDate, primaryType, now, now); err != nil {
		return nil, fmt.Errorf("insert album: %w", err)
	}
	album, err := s.getAlbumByMBID(mbid)
	if err != nil {
		return nil, fmt.Errorf("get album after insert: %w", err)
	}
	return album, nil
}

// scanAlbum scans one albumSelect row — shared by every caller below so
// the description_fetched_at NULL-handling (see Album's own doc comment)
// lives in exactly one place.
func scanAlbum(row interface{ Scan(...any) error }) (*Album, error) {
	var a Album
	var descriptionFetchedAt sql.NullTime
	if err := row.Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType,
		&a.Description, &a.Mood, &descriptionFetchedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if descriptionFetchedAt.Valid {
		a.DescriptionFetchedAt = &descriptionFetchedAt.Time
	}
	return &a, nil
}

func (s *Store) getAlbumByMBID(mbid string) (*Album, error) {
	a, err := scanAlbum(s.db.QueryRow(albumSelect+` WHERE mbid = ?`, mbid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album by mbid: %w", err)
	}
	return a, nil
}

// getAlbumByReleaseGroupMBID mirrors getAlbumByMBID, scoped to the release
// group identity GetOrCreateAlbum now uses — see its doc comment.
func (s *Store) getAlbumByReleaseGroupMBID(artistID int64, releaseGroupMBID string) (*Album, error) {
	a, err := scanAlbum(s.db.QueryRow(albumSelect+` WHERE artist_id = ? AND release_group_mbid = ?`, artistID, releaseGroupMBID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album by release group mbid: %w", err)
	}
	return a, nil
}

// DeleteAlbum deletes id outright — the album page's own "Remove album"
// action, distinct from DeleteArtist's whole-discography version. Cascades
// (per the schema's own FK setup) to the album's tracks. Deliberately does
// NOT cascade to track_files, for the same reason DeleteArtist doesn't
// (see its own comment): track_files.track_id is ON DELETE SET NULL, not
// CASCADE, so calling this before every one of the album's own track_files
// rows has already been unlinked (via SetTrackFileMatch) would silently
// orphan them — track_id goes NULL but match_status stays whatever it was.
// RemoveAlbum is the only intended caller, and it does that cleanup first.
func (s *Store) DeleteAlbum(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM albums WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete album %d: %w", id, err)
	}
	return nil
}

// ReapOrphanedAlbum deletes albumID if none of its tracks have a linked
// track_file anymore — the fix for a real dead end an album could
// otherwise land in: ListAlbumsByArtist requires a linked file to count as
// owned, and ListMissingArtistReleaseGroups excludes any release group
// that already has an albums row, matched or not. Without this, an album
// whose last file gets cleared/deleted/pruned keeps its now-empty albums
// row forever — invisible in Owned (no file), Missing (an albums row
// exists), and Wanted (already converted away by the match that created
// the row) all at once. Called after every path that can strip an album
// down to zero files: ClearMatch, DeleteTrackFile, DeleteTrackFilesMissing,
// and ScanAlbumFolder's own per-album prune.
func (s *Store) ReapOrphanedAlbum(albumID int64) error {
	var hasFiles bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM track_files tf
			JOIN tracks t ON t.id = tf.track_id
			WHERE t.album_id = ?
		)`, albumID).Scan(&hasFiles)
	if err != nil {
		return fmt.Errorf("check album %d has files: %w", albumID, err)
	}
	if hasFiles {
		return nil
	}
	return s.DeleteAlbum(albumID)
}

const albumSelect = `SELECT id, artist_id, mbid, release_group_mbid, title, release_date, primary_type, description, mood, description_fetched_at, created_at, updated_at FROM albums`

// GetAlbum returns a single album by ID, or ErrNotFound.
func (s *Store) GetAlbum(id int64) (*Album, error) {
	a, err := scanAlbum(s.db.QueryRow(albumSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get album: %w", err)
	}
	return a, nil
}

// SetAlbumDescription records id's own TheAudioDB description and mood —
// see Album.Description's own doc comment for the caching convention.
func (s *Store) SetAlbumDescription(id int64, description, mood string, fetchedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE albums SET description = ?, mood = ?, description_fetched_at = ?, updated_at = ? WHERE id = ?`,
		description, mood, fetchedAt, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("set album description: %w", err)
	}
	return nil
}

// ListAlbumsByArtist returns every album with at least one track file,
// under artistID, newest release first.
func (s *Store) ListAlbumsByArtist(artistID int64) ([]Album, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT al.id, al.artist_id, al.mbid, al.release_group_mbid, al.title, al.release_date, al.primary_type, al.created_at, al.updated_at
		FROM albums al
		JOIN tracks t ON t.album_id = al.id
		JOIN track_files tf ON tf.track_id = t.id
		WHERE al.artist_id = ?
		ORDER BY al.release_date DESC, al.title`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list albums by artist: %w", err)
	}
	defer rows.Close()

	// Non-nil empty slice so an empty result JSON-encodes to [] rather
	// than null — see Store.ListArtists' identical note.
	out := []Album{}
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.ArtistID, &a.MBID, &a.ReleaseGroupMBID, &a.Title, &a.ReleaseDate, &a.PrimaryType, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan album: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
