package plexplaylistsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cantinode/cantinode/internal/config"
	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

// fakePlexPlaylist is one playlist as the fake server itself understands it
// — just enough shape (title, updatedAt, ordered track ratingKeys) for
// every request PollOnce actually makes.
type fakePlexPlaylist struct {
	title     string
	updatedAt int64
	items     []string
}

// fakePlex is a minimal in-memory stand-in for the handful of Plex
// endpoints internal/plexplaylistsync's own sync pass calls — AudioPlaylists,
// PlaylistItems, MachineIdentifier, CreatePlaylist, DeletePlaylist, and
// AllTrackPaths' own section-tracks listing. Not a general Plex fake:
// AddPlaylistItems/RenamePlaylist aren't exercised by this package's own
// delete-and-recreate push strategy, so they're not implemented here.
type fakePlex struct {
	mu        sync.Mutex
	machineID string
	tracks    map[string]string // ratingKey -> file path, as Plex itself sees it
	playlists map[string]*fakePlexPlaylist
	nextKey   int
	requests  int
}

func newFakePlex(t *testing.T) (*fakePlex, *httptest.Server) {
	t.Helper()
	f := &fakePlex{machineID: "test-machine", tracks: map[string]string{}, playlists: map[string]*fakePlexPlaylist{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return f, srv
}

// addTrack registers one library track at path under ratingKey — the whole
// "library" AllTrackPaths sees.
func (f *fakePlex) addTrack(ratingKey, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tracks[ratingKey] = path
}

// addPlaylist seeds a pre-existing Plex playlist directly (bypassing
// CreatePlaylist), for tests that need one to exist before PollOnce ever
// runs — e.g. simulating something a real Plex user made independently.
func (f *fakePlex) addPlaylist(ratingKey, title string, updatedAt int64, items ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playlists[ratingKey] = &fakePlexPlaylist{title: title, updatedAt: updatedAt, items: items}
}

func (f *fakePlex) playlist(ratingKey string) (*fakePlexPlaylist, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.playlists[ratingKey]
	return p, ok
}

func (f *fakePlex) playlistCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.playlists)
}

func (f *fakePlex) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func parseMetadataURIKeys(uri string) []string {
	idx := strings.LastIndex(uri, "/")
	if idx < 0 || idx == len(uri)-1 {
		return nil
	}
	return strings.Split(uri[idx+1:], ",")
}

func (f *fakePlex) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		fmt.Fprintf(w, `{"MediaContainer":{"machineIdentifier":%q}}`, f.machineID)

	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/all"):
		f.mu.Lock()
		var sb strings.Builder
		sb.WriteString(`{"MediaContainer":{"Metadata":[`)
		i := 0
		for ratingKey, path := range f.tracks {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"ratingKey":%q,"Media":[{"Part":[{"file":%q}]}]}`, ratingKey, path)
			i++
		}
		sb.WriteString(`]}}`)
		f.mu.Unlock()
		w.Write([]byte(sb.String()))

	case r.Method == http.MethodPost && r.URL.Path == "/playlists" && r.URL.Query().Get("uri") != "":
		title := r.URL.Query().Get("title")
		keys := parseMetadataURIKeys(r.URL.Query().Get("uri"))
		f.mu.Lock()
		f.nextKey++
		key := "new" + strconv.Itoa(f.nextKey)
		f.playlists[key] = &fakePlexPlaylist{title: title, updatedAt: time.Now().Unix(), items: keys}
		f.mu.Unlock()
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":%q}]}}`, key)

	case r.Method == http.MethodGet && r.URL.Path == "/playlists":
		f.mu.Lock()
		var sb strings.Builder
		sb.WriteString(`{"MediaContainer":{"Metadata":[`)
		i := 0
		for key, p := range f.playlists {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"ratingKey":%q,"title":%q,"updatedAt":%d}`, key, p.title, p.updatedAt)
			i++
		}
		sb.WriteString(`]}}`)
		f.mu.Unlock()
		w.Write([]byte(sb.String()))

	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/items"):
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/playlists/"), "/items")
		p, ok := f.playlist(key)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var sb strings.Builder
		sb.WriteString(`{"MediaContainer":{"Metadata":[`)
		for i, trackKey := range p.items {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"ratingKey":%q,"playlistItemID":%d}`, trackKey, i+1)
		}
		sb.WriteString(`]}}`)
		w.Write([]byte(sb.String()))

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/playlists/"):
		key := strings.TrimPrefix(r.URL.Path, "/playlists/")
		if !strings.Contains(key, "/") {
			f.mu.Lock()
			delete(f.playlists, key)
			f.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusOK)
	}
}

// newTestService wires a Service against a fresh sqlite database and the
// given fake Plex server, with playlist sync enabled. Returns the raw *sql.DB
// too, since seedTrackFile needs to insert a root_folders row directly —
// musiclibrary.Store has no public constructor for one (root folders are
// normally created through internal/api's own validated handler).
func newTestService(t *testing.T, plexURL string) (*Service, *musiclibrary.Store, *sql.DB, *config.Config) {
	t.Helper()
	sqlDB, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	store := musiclibrary.NewStore(sqlDB)

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetPlex(config.PlexSettings{
		Enabled:             true,
		ServerURL:           plexURL,
		Token:               "test-token",
		SectionKey:          "7",
		PlaylistSyncEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	return New(store, cfg), store, sqlDB, cfg
}

// rootFolderID returns a single shared root_folders row's id, creating it
// the first time it's needed for db.
func rootFolderID(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := sqlDB.QueryRow(`SELECT id FROM root_folders LIMIT 1`).Scan(&id); err == nil {
		return id
	}
	res, err := sqlDB.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', '/music')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedTrackFile creates one owned, file-backed track at path (CantiNode's
// own idea of the path — the same string a Plex path mapping would
// translate), returning its track id — the unit both CantiNode playlists
// and path-based Plex matching operate on.
func seedTrackFile(t *testing.T, store *musiclibrary.Store, sqlDB *sql.DB, title, path string) int64 {
	t.Helper()
	artist, err := store.GetOrCreateArtist(path+"-artist-mbid", title+" Artist", title+" Artist")
	if err != nil {
		t.Fatal(err)
	}
	album, err := store.GetOrCreateAlbum(artist.ID, path+"-album-mbid", path+"-rg-mbid", title+" Album", "2020-01-01", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := store.GetOrCreateTrack(album.ID, path+"-track-mbid", title, 1, 1, 200_000, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	tf, err := store.UpsertTrackFileByPath(rootFolderID(t, sqlDB), path, 12345, "flac", 0, 200_000, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetTrackFileMatch(tf.ID, &track.ID, musiclibrary.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}
	return track.ID
}

func TestPollOnceNoopWhenSyncDisabled(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, _, cfg := newTestService(t, srv.URL)
	if err := cfg.SetPlex(config.PlexSettings{Enabled: true, ServerURL: srv.URL, Token: "t", SectionKey: "7", PlaylistSyncEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlaylist("Untouched", ""); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result != (PollResult{}) {
		t.Errorf("result = %+v, want zero value when playlist sync is disabled", result)
	}
	if f.requestCount() != 0 {
		t.Errorf("requests made to Plex = %d, want 0 when disabled", f.requestCount())
	}
}

// TestPollOncePushesNewCantiNodePlaylistToPlex covers the never-linked
// CantiNode → Plex direction: a fresh playlist with one resolvable track
// gets created on Plex, and the link is recorded.
func TestPollOncePushesNewCantiNodePlaylistToPlex(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, sqlDB, _ := newTestService(t, srv.URL)

	trackID := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	f.addTrack("plex-track-1", "/music/song-a.flac")

	p, err := store.CreatePlaylist("Road Trip", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendPlaylistItem(p.ID, trackID); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Created != 1 || result.PushedToPlex != 1 {
		t.Fatalf("result = %+v, want 1 created and pushed", result)
	}

	updated, err := store.GetPlaylist(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PlexRatingKey == "" {
		t.Fatal("PlexRatingKey was not recorded after pushing")
	}
	plexPl, ok := f.playlist(updated.PlexRatingKey)
	if !ok {
		t.Fatalf("no Plex playlist found under recorded ratingKey %q", updated.PlexRatingKey)
	}
	if plexPl.title != "Road Trip" || len(plexPl.items) != 1 || plexPl.items[0] != "plex-track-1" {
		t.Errorf("plex playlist = %+v, want title Road Trip with [plex-track-1]", plexPl)
	}
}

// TestPollOnceSkipsPushWhenNoTracksResolve: a playlist whose only track has
// no matching Plex-side file yet must not create an empty/junk Plex
// playlist — it's simply retried on a later pass once Plex catches up.
func TestPollOnceSkipsPushWhenNoTracksResolve(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, sqlDB, _ := newTestService(t, srv.URL)

	trackID := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	// Deliberately do NOT register this path with the fake Plex server.

	p, err := store.CreatePlaylist("Road Trip", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendPlaylistItem(p.ID, trackID); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Created != 0 || result.PushedToPlex != 0 {
		t.Errorf("result = %+v, want nothing pushed when no track resolves", result)
	}
	if f.playlistCount() != 0 {
		t.Errorf("plex playlist count = %d, want 0", f.playlistCount())
	}
	updated, err := store.GetPlaylist(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PlexRatingKey != "" {
		t.Errorf("PlexRatingKey = %q, want still unlinked", updated.PlexRatingKey)
	}
}

// TestPollOncePullsNewPlexPlaylistIntoCantiNode covers the Plex → CantiNode
// direction for a playlist CantiNode has never seen: it gets created
// locally with the right tracks and linked.
func TestPollOncePullsNewPlexPlaylistIntoCantiNode(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, sqlDB, _ := newTestService(t, srv.URL)

	trackID := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	f.addTrack("plex-track-1", "/music/song-a.flac")
	f.addPlaylist("existing-1", "From Plex", time.Now().Unix(), "plex-track-1")

	result := s.PollOnce(context.Background())
	if result.Created != 1 || result.PulledFromPlex != 1 {
		t.Fatalf("result = %+v, want 1 created and pulled", result)
	}

	playlists, err := store.ListPlaylists()
	if err != nil {
		t.Fatal(err)
	}
	if len(playlists) != 1 {
		t.Fatalf("playlists = %+v, want exactly 1", playlists)
	}
	got := playlists[0]
	if got.Name != "From Plex" || got.PlexRatingKey != "existing-1" {
		t.Errorf("pulled playlist = %+v, want name=From Plex ratingKey=existing-1", got)
	}
	tracks, err := store.ListPlaylistTracks(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].TrackID != trackID {
		t.Errorf("pulled tracks = %+v, want just trackID %d", tracks, trackID)
	}
}

// TestPollOncePropagatesPlexDeleteWhenConfigured: with the explicit
// propagate opt-in, a playlist deleted on Plex's own side takes its
// CantiNode counterpart down with it.
func TestPollOncePropagatesPlexDeleteWhenConfigured(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, sqlDB, cfg := newTestService(t, srv.URL)
	if err := cfg.SetPlex(config.PlexSettings{
		Enabled: true, ServerURL: srv.URL, Token: "t", SectionKey: "7",
		PlaylistSyncEnabled: true, PlaylistDeleteMode: config.PlaylistDeletePropagate,
	}); err != nil {
		t.Fatal(err)
	}
	_ = f // no track needed: this test only exercises the delete-detection branch

	trackID := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	p, err := store.CreatePlaylist("Linked", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendPlaylistItem(p.ID, trackID); err != nil {
		t.Fatal(err)
	}
	// Link it as if a previous sync pass already pushed it, then simulate
	// Plex's own copy having been deleted since (never added to f at all).
	if err := store.SetPlaylistPlexLink(p.ID, "gone-key", time.Now().Unix(), p.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Deleted != 1 {
		t.Fatalf("result = %+v, want 1 deleted", result)
	}
	if _, err := store.GetPlaylist(p.ID); !isNotFound(err) {
		t.Errorf("GetPlaylist after propagated delete: err = %v, want ErrNotFound", err)
	}
}

// TestPollOnceUnlinksOnPlexDeleteByDefault: the default (unlink) mode keeps
// CantiNode's own copy intact and just drops the link when Plex's side is
// gone — never destroys data on its own.
func TestPollOnceUnlinksOnPlexDeleteByDefault(t *testing.T) {
	_, srv := newFakePlex(t)
	s, store, sqlDB, _ := newTestService(t, srv.URL) // default PlaylistDeleteMode: unlink

	trackID := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	p, err := store.CreatePlaylist("Linked", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendPlaylistItem(p.ID, trackID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPlaylistPlexLink(p.ID, "gone-key", time.Now().Unix(), p.UpdatedAt); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Unlinked != 1 || result.Deleted != 0 {
		t.Fatalf("result = %+v, want 1 unlinked and 0 deleted", result)
	}
	got, err := store.GetPlaylist(p.ID)
	if err != nil {
		t.Fatalf("playlist should survive in unlink mode: %v", err)
	}
	if got.PlexRatingKey != "" {
		t.Errorf("PlexRatingKey = %q, want cleared", got.PlexRatingKey)
	}
}

// TestPollOnceSkipsTombstonedPlaylist confirms a playlist deleted on
// CantiNode's own side (recorded via RecordPlexPlaylistTombstone, exactly
// as internal/api's delete handler does synchronously) is never
// resurrected just because Plex's own copy still exists.
func TestPollOnceSkipsTombstonedPlaylist(t *testing.T) {
	f, srv := newFakePlex(t)
	s, store, sqlDB, _ := newTestService(t, srv.URL)

	seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
	f.addTrack("plex-track-1", "/music/song-a.flac")
	f.addPlaylist("tombstoned-1", "Still On Plex", time.Now().Unix(), "plex-track-1")

	if err := store.RecordPlexPlaylistTombstone("tombstoned-1"); err != nil {
		t.Fatal(err)
	}

	result := s.PollOnce(context.Background())
	if result.Created != 0 || result.PulledFromPlex != 0 {
		t.Errorf("result = %+v, want nothing adopted for a tombstoned ratingKey", result)
	}
	playlists, err := store.ListPlaylists()
	if err != nil {
		t.Fatal(err)
	}
	if len(playlists) != 0 {
		t.Errorf("playlists = %+v, want none created", playlists)
	}
}

// TestPollOnceConflictLastWriteWins covers both directions of the
// both-sides-changed conflict case: whichever side's own timestamp is
// newer overwrites the other.
func TestPollOnceConflictLastWriteWins(t *testing.T) {
	t.Run("cantinode side is newer", func(t *testing.T) {
		f, srv := newFakePlex(t)
		s, store, sqlDB, _ := newTestService(t, srv.URL)

		trackA := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
		trackB := seedTrackFile(t, store, sqlDB, "Song B", "/music/song-b.flac")
		f.addTrack("plex-a", "/music/song-a.flac")
		f.addTrack("plex-b", "/music/song-b.flac")

		p, err := store.CreatePlaylist("Both Changed", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendPlaylistItem(p.ID, trackA); err != nil {
			t.Fatal(err)
		}
		// Simulate an already-linked playlist whose last sync predates
		// both sides' current state.
		staleSync := time.Now().Add(-time.Hour)
		if err := store.SetPlaylistPlexLink(p.ID, "conflict-1", staleSync.Unix(), staleSync); err != nil {
			t.Fatal(err)
		}
		f.addPlaylist("conflict-1", "Old Plex Name", staleSync.Add(10*time.Minute).Unix(), "plex-a")

		// CantiNode's own edit lands after Plex's — CantiNode should win.
		if _, err := store.AppendPlaylistItem(p.ID, trackB); err != nil {
			t.Fatal(err)
		}

		result := s.PollOnce(context.Background())
		if result.PushedToPlex != 1 || result.PulledFromPlex != 0 {
			t.Fatalf("result = %+v, want the newer CantiNode side pushed", result)
		}
		updated, err := store.GetPlaylist(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		plexPl, ok := f.playlist(updated.PlexRatingKey)
		if !ok || len(plexPl.items) != 2 {
			t.Errorf("plex playlist after push = %+v (ok=%v), want both tracks", plexPl, ok)
		}
	})

	t.Run("plex side is newer", func(t *testing.T) {
		f, srv := newFakePlex(t)
		s, store, sqlDB, _ := newTestService(t, srv.URL)

		trackA := seedTrackFile(t, store, sqlDB, "Song A", "/music/song-a.flac")
		seedTrackFile(t, store, sqlDB, "Song B", "/music/song-b.flac")
		f.addTrack("plex-a", "/music/song-a.flac")
		f.addTrack("plex-b", "/music/song-b.flac")

		p, err := store.CreatePlaylist("Both Changed", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendPlaylistItem(p.ID, trackA); err != nil {
			t.Fatal(err)
		}
		staleSync := time.Now().Add(-time.Hour)
		if err := store.SetPlaylistPlexLink(p.ID, "conflict-2", staleSync.Unix(), staleSync); err != nil {
			t.Fatal(err)
		}
		// CantiNode's own edit (AppendPlaylistItem, above) already stamped
		// UpdatedAt to roughly now; Plex's own edit is recorded as
		// happening even later, so Plex should still win the conflict.
		f.addPlaylist("conflict-2", "New Plex Name", time.Now().Add(time.Hour).Unix(), "plex-a", "plex-b")

		result := s.PollOnce(context.Background())
		if result.PulledFromPlex != 1 || result.PushedToPlex != 0 {
			t.Fatalf("result = %+v, want the newer Plex side pulled", result)
		}
		updated, err := store.GetPlaylist(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Name != "New Plex Name" {
			t.Errorf("Name = %q, want pulled from Plex", updated.Name)
		}
		tracks, err := store.ListPlaylistTracks(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tracks) != 2 {
			t.Errorf("tracks after pull = %+v, want both plex-a and plex-b resolved", tracks)
		}
	})
}

func isNotFound(err error) bool {
	return errors.Is(err, musiclibrary.ErrNotFound)
}
