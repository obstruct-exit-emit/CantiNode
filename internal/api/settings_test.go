package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// TestPutNamingSettingsTakesEffectImmediately is the regression test for a
// real live bug: PUT /api/v1/settings/naming saved the new template to
// config but never reached musicScanner's own in-memory copy (only
// handlePutMusicSettings called Scanner.UpdateSettings), so Organize kept
// planning paths against the stale template until the next process
// restart — a template change that genuinely altered a file's target path
// still reported "already organized," because the plan was computed from
// the old template the file already matched.
func TestPutNamingSettingsTakesEffectImmediately(t *testing.T) {
	a := newTestAPI(t)

	rootDir := t.TempDir()
	var rf struct {
		ID int64 `json:"id"`
	}
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "music", "path": rootDir}, &rf), http.StatusCreated)

	// seedMusicAlbumFixture's own fixture values (Artist "Boards of
	// Canada", Album "Geogaddi", TrackNumber 1, Title "Ready Lets Go")
	// render, under the default template, to exactly this path — so the
	// track file starts out already "organized" under the default
	// template.
	artistID := seedMusicArtist(t, a, "naming-test")
	defaultPath := filepath.Join(rootDir, "Boards of Canada", "Geogaddi", "01 - Ready Lets Go.flac")
	_, trackFileID := seedMusicAlbumFixture(t, a, artistID, rf.ID, defaultPath)

	var preview struct {
		Path string `json:"path"`
	}
	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/trackfile/%d/organize/preview", trackFileID), nil, &preview), http.StatusOK)
	if preview.Path != defaultPath {
		t.Fatalf("preview path (default template) = %q, want %q", preview.Path, defaultPath)
	}

	// Change the template to one that produces a genuinely different path
	// for this same file (adds a {ReleaseType} folder level).
	a.want(a.call("PUT", "/api/v1/settings/naming",
		map[string]string{"musicFile": "{Artist}/{ReleaseType}/{Album}/{TrackNumber} - {Title}.{Ext}"}, nil), http.StatusOK)

	a.want(a.call("GET", fmt.Sprintf("/api/v1/music/trackfile/%d/organize/preview", trackFileID), nil, &preview), http.StatusOK)
	wantPath := filepath.Join(rootDir, "Boards of Canada", "Album", "Geogaddi", "01 - Ready Lets Go.flac")
	if preview.Path != wantPath {
		t.Errorf("preview path after naming settings change = %q, want %q (new template never reached the live scanner)", preview.Path, wantPath)
	}
}

// TestGetTagWriteSettingsDefaultsToAllEnabled confirms a fresh instance's
// GET /api/v1/settings/tagwrite reports nothing disabled — see
// config.TagWriteSettings' own doc comment on why its zero value already
// means "write everything."
func TestGetTagWriteSettingsDefaultsToAllEnabled(t *testing.T) {
	a := newTestAPI(t)
	var got map[string]any
	a.want(a.call("GET", "/api/v1/settings/tagwrite", nil, &got), http.StatusOK)
	for field, v := range got {
		if b, ok := v.(bool); ok && b {
			t.Errorf("field %q = true on a fresh instance, want every disable* flag false", field)
		}
	}
}

// TestPutTagWriteSettingsPersistsAndPropagates confirms a PUT round-trips
// through config.yaml (a later GET sees the change) — mirrors
// TestPutNamingSettingsTakesEffectImmediately's own "did the write-path
// actually reach the thing that reads it back" shape, one layer up
// (config persistence itself, not yet the live Scanner — see
// internal/musicscanner's own WriteTags toggle tests for that half).
func TestPutTagWriteSettingsPersistsAndPropagates(t *testing.T) {
	a := newTestAPI(t)

	body := map[string]bool{"disableGenre": true, "disableComposer": true}
	var put map[string]any
	a.want(a.call("PUT", "/api/v1/settings/tagwrite", body, &put), http.StatusOK)
	if put["disableGenre"] != true || put["disableComposer"] != true {
		t.Fatalf("PUT response = %+v, want disableGenre/disableComposer true", put)
	}

	var got map[string]any
	a.want(a.call("GET", "/api/v1/settings/tagwrite", nil, &got), http.StatusOK)
	if got["disableGenre"] != true || got["disableComposer"] != true {
		t.Errorf("GET after PUT = %+v, want disableGenre/disableComposer still true", got)
	}
	if got["disableTitle"] == true {
		t.Errorf("GET after PUT = %+v, want disableTitle to remain false (untouched field)", got)
	}
}
