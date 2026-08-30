// Package plexplaylistsync keeps CantiNode's own playlists and a linked
// Plex Media Server's playlists in sync, both ways: a playlist created or
// edited in either place is reflected in the other on the next pass. Plex
// has no reliable "look up by MBID" surface, so a track is matched between
// the two systems by its own on-disk file path (translated through
// config.PlexSettings.PathMappings, the same mechanism internal/plex's
// scan-notify half already uses) rather than any shared identifier.
//
// Conflict resolution is last-write-wins: each playlist records, as of its
// last successful sync, both its own CantiNode UpdatedAt and Plex's own
// reported updatedAt (musiclibrary.Playlist's PlexSyncedAt/PlexUpdatedAt).
// Comparing each side's *current* value against what was recorded then
// tells this package which side (if either) changed since — and if both
// did, whichever has the newer timestamp overwrites the other. A brand-new
// playlist with no link yet is created on the other side outright.
//
// Deleting a *linked* playlist is handled two different ways depending on
// which side noticed it: a Plex-side deletion is only ever detected here,
// during PollOnce (see the PlaylistDeleteMode branch below); a CantiNode-
// side deletion is handled synchronously in internal/api's own delete
// handler, not here, since polling for it would mean waiting an entire
// PollInterval to reflect something CantiNode itself already knows happened
// — see internal/api/playlists.go's propagatePlaylistDelete. Both paths
// honor config.PlexSettings.PlaylistDeleteMode identically.
package plexplaylistsync

import (
	"context"
	"log/slog"
	"time"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/musiclibrary"
	"github.com/cantinode/cantinode/internal/plex"
)

// PollInterval is how often a sync pass runs — frequent enough that an edit
// on either side shows up on the other within a reasonable wait, without
// hammering Plex's API (a pass costs one full-library track listing plus
// one request per playlist that actually changed).
const PollInterval = 10 * time.Minute

// Service ties the music store, config (read live each pass, so a settings
// change or newly-supplied token takes effect on the very next poll without
// a restart), and a logger together.
type Service struct {
	music  *musiclibrary.Store
	cfg    *config.Config
	logger *slog.Logger
}

func New(music *musiclibrary.Store, cfg *config.Config) *Service {
	return &Service{music: music, cfg: cfg, logger: slog.Default()}
}

// RunPeriodic syncs immediately, then again every interval until ctx is
// canceled. interval <= 0 uses PollInterval.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = PollInterval
	}
	s.PollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.PollOnce(ctx)
		}
	}
}

// PollResult summarizes one PollOnce pass, for logging/testing and — via
// internal/api's "sync now" endpoint — the Playlists page's own feedback.
type PollResult struct {
	PushedToPlex   int `json:"pushedToPlex"`
	PulledFromPlex int `json:"pulledFromPlex"`
	Created        int `json:"created"`
	Deleted        int `json:"deleted"`
	Unlinked       int `json:"unlinked"`
	Errors         int `json:"errors"`
}

// PollOnce reconciles every playlist, both directions, in one pass. A
// no-op (feature off, or connection not configured) returns a zero
// PollResult rather than an error — this runs unattended, so there's no
// caller to report an error to; problems are logged and reflected in
// Errors instead.
func (s *Service) PollOnce(ctx context.Context) PollResult {
	var result PollResult
	settings := s.cfg.PlexSettings()
	if !settings.PlaylistSyncReady() {
		return result
	}

	client := plex.NewClient(settings.ServerURL, settings.Token)
	machineID, err := client.MachineIdentifier(ctx)
	if err != nil {
		s.logger.Warn("plexplaylistsync: get machine identifier", "error", err)
		result.Errors++
		return result
	}
	plexPathToKey, err := client.AllTrackPaths(ctx, settings.SectionKey)
	if err != nil {
		s.logger.Warn("plexplaylistsync: list track paths", "error", err)
		result.Errors++
		return result
	}
	keyToPlexPath := make(map[string]string, len(plexPathToKey))
	for path, key := range plexPathToKey {
		keyToPlexPath[key] = path
	}
	reverseMappings := reverseTranslate(settings.PathMappings)

	cnPlaylists, err := s.music.ListPlaylists()
	if err != nil {
		s.logger.Warn("plexplaylistsync: list playlists", "error", err)
		result.Errors++
		return result
	}
	plexPlaylists, err := client.AudioPlaylists(ctx)
	if err != nil {
		s.logger.Warn("plexplaylistsync: list plex playlists", "error", err)
		result.Errors++
		return result
	}
	plexByKey := make(map[string]plex.PlaylistSummary, len(plexPlaylists))
	for _, p := range plexPlaylists {
		plexByKey[p.RatingKey] = p
	}

	handled := make(map[string]bool, len(cnPlaylists))
	for _, cn := range cnPlaylists {
		if ctx.Err() != nil {
			return result
		}
		if cn.PlexRatingKey == "" {
			s.pushNew(ctx, client, machineID, settings, cn, plexPathToKey, &result)
			continue
		}
		handled[cn.PlexRatingKey] = true
		plexPl, ok := plexByKey[cn.PlexRatingKey]
		if !ok {
			s.handlePlexSideDeleted(cn, settings, &result)
			continue
		}

		cnChanged := cn.PlexSyncedAt == nil || cn.UpdatedAt.After(*cn.PlexSyncedAt)
		plexChanged := plexPl.UpdatedAt > cn.PlexUpdatedAt
		switch {
		case !cnChanged && !plexChanged:
			// Neither side has moved since the last sync — nothing to do.
		case cnChanged && !plexChanged:
			s.pushExisting(ctx, client, machineID, settings, cn, plexPl.RatingKey, plexPathToKey, &result)
		case plexChanged && !cnChanged:
			s.pull(ctx, client, cn, plexPl, keyToPlexPath, reverseMappings, &result)
		default:
			// Both sides changed since the last sync — last write wins.
			if cn.UpdatedAt.Unix() >= plexPl.UpdatedAt {
				s.pushExisting(ctx, client, machineID, settings, cn, plexPl.RatingKey, plexPathToKey, &result)
			} else {
				s.pull(ctx, client, cn, plexPl, keyToPlexPath, reverseMappings, &result)
			}
		}
	}

	for _, plexPl := range plexPlaylists {
		if ctx.Err() != nil {
			return result
		}
		if handled[plexPl.RatingKey] {
			continue
		}
		tombstoned, err := s.music.IsPlexPlaylistTombstoned(plexPl.RatingKey)
		if err != nil {
			s.logger.Warn("plexplaylistsync: checking tombstone", "ratingKey", plexPl.RatingKey, "error", err)
			result.Errors++
			continue
		}
		if tombstoned {
			continue
		}
		s.pullNew(ctx, client, plexPl, keyToPlexPath, reverseMappings, &result)
	}

	return result
}

// handlePlexSideDeleted reacts to a linked playlist that's vanished from
// Plex's own list — the one delete direction PollOnce itself can detect (a
// CantiNode-side delete is handled synchronously elsewhere; see the package
// doc comment).
func (s *Service) handlePlexSideDeleted(cn musiclibrary.Playlist, settings config.PlexSettings, result *PollResult) {
	if settings.PlaylistDeletePropagates() {
		if err := s.music.DeletePlaylist(cn.ID); err != nil {
			s.logger.Warn("plexplaylistsync: deleting playlist after plex-side delete", "playlist", cn.Name, "error", err)
			result.Errors++
			return
		}
		result.Deleted++
		return
	}
	if err := s.music.ClearPlaylistPlexLink(cn.ID); err != nil {
		s.logger.Warn("plexplaylistsync: unlinking playlist after plex-side delete", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	result.Unlinked++
}

// collectRatingKeys resolves cn's own current tracks to Plex ratingKeys —
// any track with no current file, or whose file Plex doesn't have (not yet
// scanned there, or genuinely absent), is simply skipped rather than
// failing the whole push.
func (s *Service) collectRatingKeys(cn musiclibrary.Playlist, settings config.PlexSettings, plexPathToKey map[string]string) ([]string, error) {
	tracks, err := s.music.ListPlaylistTracks(cn.ID)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, t := range tracks {
		if t.Path == "" {
			continue
		}
		plexPath := config.TranslatePath(settings.PathMappings, t.Path)
		if key, ok := plexPathToKey[plexPath]; ok {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// pushNew creates a brand-new Plex playlist for a CantiNode playlist that's
// never been linked. Skipped (retried next pass) when none of its tracks
// currently resolve to a Plex item — Plex has no API for creating an empty
// playlist, and creating a one-track placeholder just to keep appending to
// it would need its own separate add-items call anyway with no real
// benefit.
func (s *Service) pushNew(ctx context.Context, client *plex.Client, machineID string, settings config.PlexSettings, cn musiclibrary.Playlist, plexPathToKey map[string]string, result *PollResult) {
	keys, err := s.collectRatingKeys(cn, settings, plexPathToKey)
	if err != nil {
		s.logger.Warn("plexplaylistsync: listing tracks to push", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	if len(keys) == 0 {
		return
	}
	ratingKey, err := client.CreatePlaylist(ctx, machineID, cn.Name, keys)
	if err != nil {
		s.logger.Warn("plexplaylistsync: creating plex playlist", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	// Plex's own updatedAt for the playlist just created isn't known
	// without a further round trip — wall-clock time is close enough; a
	// spurious few-second mismatch on the very next pass just costs one
	// harmless, idempotent pull of the same content this pass just pushed.
	if err := s.music.SetPlaylistPlexLink(cn.ID, ratingKey, time.Now().Unix(), cn.UpdatedAt); err != nil {
		s.logger.Warn("plexplaylistsync: recording new plex link", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	result.Created++
	result.PushedToPlex++
}

// pushExisting overwrites an already-linked Plex playlist with CantiNode's
// current content. Plex has no "replace this playlist's items" call, so
// this deletes and recreates it — its ratingKey changes, which is why the
// link is rewritten to the new one afterward.
func (s *Service) pushExisting(ctx context.Context, client *plex.Client, machineID string, settings config.PlexSettings, cn musiclibrary.Playlist, oldRatingKey string, plexPathToKey map[string]string, result *PollResult) {
	keys, err := s.collectRatingKeys(cn, settings, plexPathToKey)
	if err != nil {
		s.logger.Warn("plexplaylistsync: listing tracks to push", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	if len(keys) == 0 {
		// Every track is currently unresolvable on Plex's side — skip
		// rather than destroying the existing Plex playlist over what may
		// well be transient (Plex hasn't scanned a new file in yet, etc).
		return
	}
	if err := client.DeletePlaylist(ctx, oldRatingKey); err != nil {
		s.logger.Warn("plexplaylistsync: replacing plex playlist (delete)", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	newRatingKey, err := client.CreatePlaylist(ctx, machineID, cn.Name, keys)
	if err != nil {
		s.logger.Warn("plexplaylistsync: replacing plex playlist (create)", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	if err := s.music.SetPlaylistPlexLink(cn.ID, newRatingKey, time.Now().Unix(), cn.UpdatedAt); err != nil {
		s.logger.Warn("plexplaylistsync: recording plex link", "playlist", cn.Name, "error", err)
		result.Errors++
		return
	}
	result.PushedToPlex++
}

// resolveLocalTrackIDs resolves a Plex playlist's own current items back to
// CantiNode track ids — an item Plex has that CantiNode doesn't (not yet
// scanned in, or a file type CantiNode's own scanner skipped) is simply
// omitted rather than failing the whole pull.
func (s *Service) resolveLocalTrackIDs(ctx context.Context, client *plex.Client, ratingKey string, keyToPlexPath map[string]string, reverseMappings []config.PathMapping) ([]int64, error) {
	items, err := client.PlaylistItems(ctx, ratingKey)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, item := range items {
		plexPath, ok := keyToPlexPath[item.RatingKey]
		if !ok {
			continue
		}
		localPath := config.TranslatePath(reverseMappings, plexPath)
		tf, err := s.music.GetTrackFileByPath(localPath)
		if err != nil {
			continue
		}
		if tf.TrackID == nil {
			continue
		}
		ids = append(ids, *tf.TrackID)
	}
	return ids, nil
}

// pull replaces an already-linked CantiNode playlist's content with Plex's
// current one (and its name, if that changed too).
func (s *Service) pull(ctx context.Context, client *plex.Client, cn musiclibrary.Playlist, plexPl plex.PlaylistSummary, keyToPlexPath map[string]string, reverseMappings []config.PathMapping, result *PollResult) {
	trackIDs, err := s.resolveLocalTrackIDs(ctx, client, plexPl.RatingKey, keyToPlexPath, reverseMappings)
	if err != nil {
		s.logger.Warn("plexplaylistsync: listing plex playlist items", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	if cn.Name != plexPl.Title {
		if err := s.music.UpdatePlaylist(cn.ID, plexPl.Title, cn.Description); err != nil {
			s.logger.Warn("plexplaylistsync: renaming playlist", "playlist", cn.Name, "error", err)
		}
	}
	syncedAt, err := s.music.ReplacePlaylistItems(cn.ID, trackIDs)
	if err != nil {
		s.logger.Warn("plexplaylistsync: replacing playlist items", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	if err := s.music.SetPlaylistPlexLink(cn.ID, plexPl.RatingKey, plexPl.UpdatedAt, syncedAt); err != nil {
		s.logger.Warn("plexplaylistsync: recording plex link", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	result.PulledFromPlex++
}

// pullNew creates a new CantiNode playlist for a Plex playlist CantiNode
// has never seen before. Skipped (retried next pass) when none of its
// tracks currently resolve to a CantiNode one.
func (s *Service) pullNew(ctx context.Context, client *plex.Client, plexPl plex.PlaylistSummary, keyToPlexPath map[string]string, reverseMappings []config.PathMapping, result *PollResult) {
	trackIDs, err := s.resolveLocalTrackIDs(ctx, client, plexPl.RatingKey, keyToPlexPath, reverseMappings)
	if err != nil {
		s.logger.Warn("plexplaylistsync: listing plex playlist items", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	if len(trackIDs) == 0 {
		return
	}
	cn, err := s.music.CreatePlaylistFromPlex(plexPl.Title, "")
	if err != nil {
		s.logger.Warn("plexplaylistsync: creating playlist", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	syncedAt, err := s.music.ReplacePlaylistItems(cn.ID, trackIDs)
	if err != nil {
		s.logger.Warn("plexplaylistsync: replacing playlist items", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	if err := s.music.SetPlaylistPlexLink(cn.ID, plexPl.RatingKey, plexPl.UpdatedAt, syncedAt); err != nil {
		s.logger.Warn("plexplaylistsync: recording plex link", "playlist", plexPl.Title, "error", err)
		result.Errors++
		return
	}
	result.Created++
	result.PulledFromPlex++
}

// reverseTranslate swaps every mapping's remote/local prefixes, so
// config.TranslatePath can be reused to go from Plex's own path back to
// CantiNode's (PathMappings itself only ever translates CantiNode → Plex).
func reverseTranslate(mappings []config.PathMapping) []config.PathMapping {
	out := make([]config.PathMapping, len(mappings))
	for i, m := range mappings {
		out[i] = config.PathMapping{RemotePrefix: m.LocalPrefix, LocalPrefix: m.RemotePrefix}
	}
	return out
}
