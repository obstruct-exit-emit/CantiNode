package plex

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestAudioPlaylists(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{"ratingKey":"100","title":"Road Trip","updatedAt":1700000000}
		]}}`))
	})

	got, err := c.AudioPlaylists(t.Context())
	if err != nil {
		t.Fatalf("AudioPlaylists: %v", err)
	}
	if len(got) != 1 || got[0].RatingKey != "100" || got[0].Title != "Road Trip" || got[0].UpdatedAt != 1700000000 {
		t.Errorf("got = %+v", got)
	}
	if gotQuery != "playlistType=audio" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestPlaylistItems(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[
			{"ratingKey":"456","playlistItemID":789},
			{"ratingKey":"457","playlistItemID":790}
		]}}`))
	})

	got, err := c.PlaylistItems(t.Context(), "100")
	if err != nil {
		t.Fatalf("PlaylistItems: %v", err)
	}
	if gotPath != "/playlists/100/items" {
		t.Errorf("path = %q", gotPath)
	}
	if len(got) != 2 || got[0].RatingKey != "456" || got[0].PlaylistItemID != "789" {
		t.Errorf("got = %+v", got)
	}
}

func TestMachineIdentifier(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"abc-123"}}`))
	})
	got, err := c.MachineIdentifier(t.Context())
	if err != nil {
		t.Fatalf("MachineIdentifier: %v", err)
	}
	if got != "abc-123" {
		t.Errorf("got = %q", got)
	}
}

func TestCreatePlaylistBuildsMetadataURI(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"200","title":"New List"}]}}`))
	})

	ratingKey, err := c.CreatePlaylist(t.Context(), "machine-id", "New List", []string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if ratingKey != "200" {
		t.Errorf("ratingKey = %q, want 200", ratingKey)
	}
	wantURI := "uri=server%3A%2F%2Fmachine-id%2Fcom.plexapp.plugins.library%2Flibrary%2Fmetadata%2F1%2C2%2C3"
	if !strings.Contains(gotQuery, wantURI) {
		t.Errorf("query = %q, want it to contain %q", gotQuery, wantURI)
	}
}

func TestCreatePlaylistRequiresTracks(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not make a request with no tracks")
	})
	if _, err := c.CreatePlaylist(t.Context(), "machine-id", "Empty", nil); err == nil {
		t.Fatal("expected an error for an empty track list")
	}
}

func TestRemovePlaylistItemAndDeletePlaylistUseDelete(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	if err := c.RemovePlaylistItem(t.Context(), "100", "789"); err != nil {
		t.Fatalf("RemovePlaylistItem: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/playlists/100/items/789" {
		t.Errorf("method=%s path=%s", gotMethod, gotPath)
	}

	if err := c.DeletePlaylist(t.Context(), "100"); err != nil {
		t.Fatalf("DeletePlaylist: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/playlists/100" {
		t.Errorf("method=%s path=%s", gotMethod, gotPath)
	}
}

// TestAllTrackPathsPaginates is the regression test for the pagination
// loop: a section with exactly one page's worth of tracks plus one more
// must issue a second request (X-Plex-Container-Start advancing), and
// stop once a page comes back short of a full page.
func TestAllTrackPathsPaginates(t *testing.T) {
	var starts []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		starts = append(starts, r.Header.Get("X-Plex-Container-Start"))
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Plex-Container-Start") == "0" {
			// A full page (trackPathsPageSize items) to force a second request.
			var sb strings.Builder
			sb.WriteString(`{"MediaContainer":{"Metadata":[`)
			for i := 0; i < trackPathsPageSize; i++ {
				if i > 0 {
					sb.WriteByte(',')
				}
				n := strconv.Itoa(i)
				sb.WriteString(`{"ratingKey":"k` + n + `","Media":[{"Part":[{"file":"/music/track` + n + `.flac"}]}]}`)
			}
			sb.WriteString(`]}}`)
			w.Write([]byte(sb.String()))
			return
		}
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"kLast","Media":[{"Part":[{"file":"/music/last.flac"}]}]}]}}`))
	})

	got, err := c.AllTrackPaths(t.Context(), "7")
	if err != nil {
		t.Fatalf("AllTrackPaths: %v", err)
	}
	if len(starts) != 2 {
		t.Fatalf("requests made = %d, want 2 (one full page, one short page ending the loop)", len(starts))
	}
	if got["/music/last.flac"] != "kLast" {
		t.Errorf("got[/music/last.flac] = %q, want kLast", got["/music/last.flac"])
	}
	if len(got) != trackPathsPageSize+1 {
		t.Errorf("len(got) = %d, want %d", len(got), trackPathsPageSize+1)
	}
}
