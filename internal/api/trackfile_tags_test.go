package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestGetTrackFileTagsRequiresValidID(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("GET", "/api/v1/music/trackfile/not-a-number/tags", nil, nil), http.StatusBadRequest)
}

func TestGetTrackFileTagsNotFound(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("GET", "/api/v1/music/trackfile/999999/tags", nil, nil), http.StatusNotFound)
}

// TestGetTrackFileTagsReadsFreshFromDisk is the happy-path test: the
// handler must read the file's own embedded tags live off disk, not
// whatever the track_files.tags_json snapshot says — the whole reason
// this endpoint exists (see its own doc comment) is that the snapshot
// goes stale after a "Write tags" call, so a test asserting against a
// seeded tags_json value instead of the real file would miss exactly the
// bug this is meant to avoid.
func TestGetTrackFileTagsReadsFreshFromDisk(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	src, err := os.ReadFile(filepath.Join("testdata", "sample_tagged.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	trackPath := filepath.Join(rootDir, "sample_tagged.mp3")
	if err := os.WriteFile(trackPath, src, 0o644); err != nil {
		t.Fatal(err)
	}

	_, trackFileID := seedMusicAlbumFixture(t, a, seedMusicArtist(t, a, "tags-test"), rf.ID, trackPath)

	var tags struct {
		Title       string `json:"title"`
		Artist      string `json:"artist"`
		Album       string `json:"album"`
		TrackNumber int    `json:"trackNumber"`
		Year        int    `json:"year"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/trackfile/%d/tags", trackFileID), nil, &tags), http.StatusOK)

	// These are sample_tagged.mp3's own real, pre-existing embedded
	// values (from dhowden/tag's testdata/with_tags fixture) — not
	// anything seedMusicAlbumFixture set in the database, proving the
	// response came from actually reading the file.
	if tags.Title != "Test Title" {
		t.Errorf("Title = %q, want %q", tags.Title, "Test Title")
	}
	if tags.Artist != "Test Artist" {
		t.Errorf("Artist = %q, want %q", tags.Artist, "Test Artist")
	}
	if tags.Album != "Test Album" {
		t.Errorf("Album = %q, want %q", tags.Album, "Test Album")
	}
	if tags.TrackNumber != 3 {
		t.Errorf("TrackNumber = %d, want 3", tags.TrackNumber)
	}
	if tags.Year != 2000 {
		t.Errorf("Year = %d, want 2000", tags.Year)
	}
}
