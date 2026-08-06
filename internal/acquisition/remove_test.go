package acquisition

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/prowlarr"
	"github.com/cantinode/cantinode/internal/qbittorrent"
)

// removeTestFixtures wires a Service against a real in-memory database
// with one artist owning one track file (matched), for RemoveArtist's own
// tests. Returns the artist, the on-disk file path, and its track_files
// row ID.
func removeTestFixtures(t *testing.T) (s *Service, db *database.DB, artist *database.Artist, filePath string, trackFileID int64) {
	t.Helper()
	s, db = newTestService(t, nil)
	ctx := t.Context()

	artist, err := db.GetOrCreateArtist(ctx, "a-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}
	album, err := db.GetOrCreateAlbum(ctx, artist.ID, "al-mbid", "rg-mbid", "Geogaddi", "2002-02-04", "Album")
	if err != nil {
		t.Fatal(err)
	}
	track, err := db.GetOrCreateTrack(ctx, album.ID, "t-mbid", "Alpha and Omega", 3, 1, 200000)
	if err != nil {
		t.Fatal(err)
	}

	rf, err := db.CreateRootFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	filePath = filepath.Join(rf.Path, "song.flac")
	if err := os.WriteFile(filePath, []byte("fake audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	tf, err := db.UpsertTrackFileByPath(ctx, rf.ID, filePath, 100, "flac", 0, 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTrackFileMatch(ctx, tf.ID, &track.ID, database.StatusMatched, 1.0); err != nil {
		t.Fatal(err)
	}

	return s, db, artist, filePath, tf.ID
}

// TestRemoveArtistKeepsFilesUnlinksMatch is the deleteFiles=false path:
// the artist/albums/tracks are gone, but the file itself survives on disk
// and its track_files row reverts to unmatched rather than being deleted
// or left as a stale "matched" ghost.
func TestRemoveArtistKeepsFilesUnlinksMatch(t *testing.T) {
	s, db, artist, filePath, trackFileID := removeTestFixtures(t)
	ctx := t.Context()

	if err := s.RemoveArtist(ctx, artist.ID, false); err != nil {
		t.Fatalf("RemoveArtist: %v", err)
	}

	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("file should still be on disk: %v", err)
	}

	tf, err := db.GetTrackFile(ctx, trackFileID)
	if err != nil {
		t.Fatalf("GetTrackFile: %v", err)
	}
	if tf.MatchStatus != database.StatusUnmatched || tf.TrackID != nil {
		t.Errorf("track file = %+v, want unmatched with no track_id", tf)
	}
	if tf.Path != filePath {
		t.Errorf("track file path = %q, want unchanged %q", tf.Path, filePath)
	}

	if _, err := db.GetArtist(ctx, artist.ID); err != database.ErrNotFound {
		t.Errorf("GetArtist after remove: err = %v, want ErrNotFound", err)
	}
}

// TestRemoveArtistDeleteFilesRemovesFromDiskAndRow is the
// deleteFiles=true path: the file is actually gone, both on disk and its
// own row.
func TestRemoveArtistDeleteFilesRemovesFromDiskAndRow(t *testing.T) {
	s, db, artist, filePath, trackFileID := removeTestFixtures(t)
	ctx := t.Context()

	if err := s.RemoveArtist(ctx, artist.ID, true); err != nil {
		t.Fatalf("RemoveArtist: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("file should be gone from disk, stat err = %v", err)
	}
	if _, err := db.GetTrackFile(ctx, trackFileID); err != database.ErrNotFound {
		t.Errorf("GetTrackFile after remove: err = %v, want ErrNotFound", err)
	}
	if _, err := db.GetArtist(ctx, artist.ID); err != database.ErrNotFound {
		t.Errorf("GetArtist after remove: err = %v, want ErrNotFound", err)
	}
}

// TestRemoveArtistBestEffortCancelsInFlightDownload proves an in-flight
// download for one of the artist's wanted albums gets canceled (removed
// from the download client, its own row gone) as part of removing the
// artist — a torrent still actively downloading for an artist that's
// about to stop existing shouldn't keep running orphaned.
func TestRemoveArtistBestEffortCancelsInFlightDownload(t *testing.T) {
	s, db, artist, _, _ := removeTestFixtures(t)
	ctx := t.Context()

	w, err := db.GetOrCreateWantedAlbum(ctx, artist.ID, "rg-2", "Another Album", "Album", "1999")
	if err != nil {
		t.Fatal(err)
	}

	pwSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(pwSrv.Close)
	qb := newFakeQBittorrent("qb-key")
	qbURL := qb.start(t)
	s.UpdateClients(prowlarr.NewClient(pwSrv.URL, "key", "ua"), qbittorrent.NewClient(qbURL, "cantinode", "qb-key"), nil)

	rel := prowlarr.Release{
		Title:     "Boards of Canada - Another Album [FLAC]",
		Protocol:  prowlarr.ProtocolTorrent,
		MagnetURL: "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12",
	}
	d, err := s.GrabRelease(ctx, w.ID, rel)
	if err != nil {
		t.Fatalf("GrabRelease: %v", err)
	}
	if _, ok := qb.states[strings.ToLower(d.ClientID)]; !ok {
		t.Fatal("fake qBittorrent should have the torrent before remove")
	}

	if err := s.RemoveArtist(ctx, artist.ID, false); err != nil {
		t.Fatalf("RemoveArtist: %v", err)
	}

	if _, ok := qb.states[d.ClientID]; ok {
		t.Error("torrent should have been removed from the download client")
	}
	if _, err := db.GetDownload(ctx, d.ID); err != database.ErrNotFound {
		t.Errorf("GetDownload after remove: err = %v, want ErrNotFound", err)
	}
}
