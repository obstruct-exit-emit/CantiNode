package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PlaylistSummary is one of Plex's own playlists (GET /playlists) —
// enough for internal/plexplaylistsync to decide whether it (or its
// CantiNode counterpart) changed since the last sync.
type PlaylistSummary struct {
	RatingKey string
	Title     string
	// UpdatedAt is Plex's own last-modified time for this playlist, unix
	// seconds — compared against Playlist.PlexUpdatedAt to tell whether
	// Plex's side has changed since the last sync.
	UpdatedAt int64
}

// PlaylistItem is one track entry within a Plex playlist.
type PlaylistItem struct {
	// PlaylistItemID identifies this entry *within the playlist* — needed
	// for RemovePlaylistItem, distinct from RatingKey (the underlying
	// track's own identity, shared across every playlist it appears in).
	PlaylistItemID string
	RatingKey      string
}

type playlistsResponse struct {
	MediaContainer struct {
		Metadata []struct {
			RatingKey string `json:"ratingKey"`
			Title     string `json:"title"`
			UpdatedAt int64  `json:"updatedAt"`
			Smart     bool   `json:"smart"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// AudioPlaylists returns every *ordinary* music playlist this server knows
// about — Plex's own "smart" (rule-based) playlists (the built-in "All
// Music", "Recently Added", "❤️ Tracks", etc, plus any user-defined smart
// playlist) are excluded, since there's nothing for a two-way sync to
// reconcile against one: its membership is computed by Plex itself from a
// rule, not a stored item list, so treating it as "new" would try to mirror
// Plex's entire library (or whatever the rule matches) into CantiNode as a
// literal playlist on the very first sync pass.
func (c *Client) AudioPlaylists(ctx context.Context) ([]PlaylistSummary, error) {
	body, err := c.get(ctx, "/playlists", url.Values{"playlistType": {"audio"}})
	if err != nil {
		return nil, err
	}
	var resp playlistsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode playlists: %w", err)
	}
	out := make([]PlaylistSummary, 0, len(resp.MediaContainer.Metadata))
	for _, m := range resp.MediaContainer.Metadata {
		if m.Smart {
			continue
		}
		out = append(out, PlaylistSummary{RatingKey: m.RatingKey, Title: m.Title, UpdatedAt: m.UpdatedAt})
	}
	return out, nil
}

type playlistItemsResponse struct {
	MediaContainer struct {
		Metadata []struct {
			RatingKey      string `json:"ratingKey"`
			PlaylistItemID int64  `json:"playlistItemID"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// PlaylistItems returns ratingKey's own track list, in order.
func (c *Client) PlaylistItems(ctx context.Context, ratingKey string) ([]PlaylistItem, error) {
	body, err := c.get(ctx, "/playlists/"+url.PathEscape(ratingKey)+"/items", nil)
	if err != nil {
		return nil, err
	}
	var resp playlistItemsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode playlist items: %w", err)
	}
	out := make([]PlaylistItem, 0, len(resp.MediaContainer.Metadata))
	for _, m := range resp.MediaContainer.Metadata {
		out = append(out, PlaylistItem{PlaylistItemID: strconv.FormatInt(m.PlaylistItemID, 10), RatingKey: m.RatingKey})
	}
	return out, nil
}

// MachineIdentifier returns this server's own unique id — required to
// build the server:// URI CreatePlaylist/AddPlaylistItems need to
// reference tracks by ratingKey.
func (c *Client) MachineIdentifier(ctx context.Context) (string, error) {
	body, err := c.get(ctx, "/", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decode server identity: %w", err)
	}
	if resp.MediaContainer.MachineIdentifier == "" {
		return "", fmt.Errorf("plex: server did not report a machine identifier")
	}
	return resp.MediaContainer.MachineIdentifier, nil
}

// metadataURI builds the server://{machineIdentifier}/... URI
// CreatePlaylist/AddPlaylistItems use to reference tracks by ratingKey —
// the one URI shape shared by both.
func metadataURI(machineIdentifier string, trackRatingKeys []string) string {
	return fmt.Sprintf("server://%s/com.plexapp.plugins.library/library/metadata/%s",
		machineIdentifier, strings.Join(trackRatingKeys, ","))
}

// CreatePlaylist creates a new audio playlist named title containing
// trackRatingKeys, returning its own new ratingKey. trackRatingKeys must
// be non-empty — Plex has no API for creating an empty regular playlist.
func (c *Client) CreatePlaylist(ctx context.Context, machineIdentifier, title string, trackRatingKeys []string) (string, error) {
	if len(trackRatingKeys) == 0 {
		return "", fmt.Errorf("plex: cannot create a playlist with no tracks")
	}
	body, err := c.post(ctx, "/playlists", url.Values{
		"type":  {"audio"},
		"title": {title},
		"smart": {"0"},
		"uri":   {metadataURI(machineIdentifier, trackRatingKeys)},
	})
	if err != nil {
		return "", err
	}
	var resp playlistsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("decode created playlist: %w", err)
	}
	if len(resp.MediaContainer.Metadata) == 0 {
		return "", fmt.Errorf("plex: create playlist %q returned no playlist", title)
	}
	return resp.MediaContainer.Metadata[0].RatingKey, nil
}

// AddPlaylistItems appends trackRatingKeys to the end of ratingKey's own
// playlist, in order.
func (c *Client) AddPlaylistItems(ctx context.Context, machineIdentifier, ratingKey string, trackRatingKeys []string) error {
	if len(trackRatingKeys) == 0 {
		return nil
	}
	_, err := c.put(ctx, "/playlists/"+url.PathEscape(ratingKey)+"/items", url.Values{
		"uri": {metadataURI(machineIdentifier, trackRatingKeys)},
	})
	return err
}

// RemovePlaylistItem removes one entry (by its own PlaylistItemID, not
// the underlying track's ratingKey) from ratingKey's own playlist.
func (c *Client) RemovePlaylistItem(ctx context.Context, ratingKey, playlistItemID string) error {
	return c.delete(ctx, "/playlists/"+url.PathEscape(ratingKey)+"/items/"+url.PathEscape(playlistItemID))
}

// RenamePlaylist sets ratingKey's own playlist title.
func (c *Client) RenamePlaylist(ctx context.Context, ratingKey, title string) error {
	_, err := c.put(ctx, "/playlists/"+url.PathEscape(ratingKey), url.Values{"title": {title}})
	return err
}

// DeletePlaylist removes ratingKey's own playlist entirely.
func (c *Client) DeletePlaylist(ctx context.Context, ratingKey string) error {
	return c.delete(ctx, "/playlists/"+url.PathEscape(ratingKey))
}

// trackPathsPageSize bounds each page of AllTrackPaths' own paginated
// walk of a library section's full track list — a real music library can
// hold tens of thousands of tracks, too many to safely assume fit in one
// response.
const trackPathsPageSize = 500

type sectionTracksResponse struct {
	MediaContainer struct {
		TotalSize int `json:"totalSize"`
		Metadata  []struct {
			RatingKey string `json:"ratingKey"`
			Media     []struct {
				Part []struct {
					File string `json:"file"`
				} `json:"Part"`
			} `json:"Media"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// AllTrackPaths returns every track in sectionKey's own library, mapped
// by its own on-disk file path (exactly as Plex itself sees it) to its
// ratingKey — the whole-library map internal/plexplaylistsync builds
// once per sync pass to resolve a CantiNode track_file's path to the Plex
// item it corresponds to (and vice versa), since Plex has no "look up a
// track by path" endpoint of its own. Paginated internally; the caller
// never sees partial results.
func (c *Client) AllTrackPaths(ctx context.Context, sectionKey string) (map[string]string, error) {
	out := map[string]string{}
	for start := 0; ; start += trackPathsPageSize {
		body, err := c.getPaged(ctx, "/library/sections/"+url.PathEscape(sectionKey)+"/all",
			url.Values{"type": {"10"}}, start, trackPathsPageSize) // type 10 = track
		if err != nil {
			return nil, fmt.Errorf("list section %s tracks (offset %d): %w", sectionKey, start, err)
		}
		var resp sectionTracksResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode section tracks: %w", err)
		}
		for _, m := range resp.MediaContainer.Metadata {
			for _, media := range m.Media {
				for _, part := range media.Part {
					if part.File != "" {
						out[part.File] = m.RatingKey
					}
				}
			}
		}
		if len(resp.MediaContainer.Metadata) < trackPathsPageSize {
			break
		}
	}
	return out, nil
}
