package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cantinode/cantinode/internal/config"
)

// TestMusicSettingsHotReloadMusicBrainzBaseURL is the regression test for a
// real "saved but not applied" bug: s.mb is constructed once at startup
// (see NewRouter) baked with whatever MusicBrainzBaseURL config held then.
// PUT /api/v1/settings/music saved a changed value to config.yaml just
// fine, but every live artist search kept hitting the OLD base URL until
// the process restarted, silently — this directly mirrors
// TestPutNamingSettingsTakesEffectImmediately's own bug shape, just for the
// MusicBrainz client instead of the scanner. A settings change must reach
// the already-running client immediately, no restart. The AudioDB/Last.fm
// API key siblings get the equivalent coverage directly at the client
// level (UpdateAPIKey) in their own packages, since neither has a settings
// field to redirect its base URL at a fake server the way MusicBrainz does.
func TestMusicSettingsHotReloadMusicBrainzBaseURL(t *testing.T) {
	a := newTestAPI(t)

	var hit bool
	fakeMB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"artist-count": 1,
			"artists":      []map[string]any{{"id": "artist-mbid", "name": "Boards of Canada", "score": 100}},
		})
	}))
	defer fakeMB.Close()

	var settings config.MusicSettings
	a.want(a.call("GET", "/api/v1/settings/music", nil, &settings), http.StatusOK)
	settings.MusicBrainzBaseURL = fakeMB.URL
	a.want(a.call("PUT", "/api/v1/settings/music", settings, nil), http.StatusOK)

	var results []map[string]any
	a.want(a.call("GET", "/api/v1/music/artist/search?query=Boards+of+Canada", nil, &results), http.StatusOK)

	if !hit {
		t.Fatal("artist search never reached the new MusicBrainz base URL — the settings change wasn't applied to the live client")
	}
	if len(results) != 1 || results[0]["name"] != "Boards of Canada" {
		t.Errorf("results = %+v, want the fixture artist from the new base URL", results)
	}
}
