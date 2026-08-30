package api

import (
	"net/http"
	"testing"

	"github.com/cantinode/cantinode/internal/config"
)

func TestGetPutPlexSettingsRoundTrip(t *testing.T) {
	a := newTestAPI(t)

	var got config.PlexSettings
	a.want(a.call("GET", "/api/v1/settings/plex", nil, &got), http.StatusOK)
	if got.Enabled {
		t.Errorf("Enabled = true, want the default false")
	}

	want := config.PlexSettings{
		Enabled:    true,
		ServerURL:  "http://192.168.1.10:32400",
		Token:      "my-token",
		SectionKey: "2",
	}
	var updated config.PlexSettings
	a.want(a.call("PUT", "/api/v1/settings/plex", want, &updated), http.StatusOK)
	if updated.Enabled != want.Enabled || updated.ServerURL != want.ServerURL ||
		updated.Token != want.Token || updated.SectionKey != want.SectionKey {
		t.Errorf("updated = %+v, want %+v", updated, want)
	}

	var reGet config.PlexSettings
	a.want(a.call("GET", "/api/v1/settings/plex", nil, &reGet), http.StatusOK)
	if reGet.ServerURL != want.ServerURL {
		t.Errorf("GET after PUT = %+v, want %+v", reGet, want)
	}
}

// TestListPlexSectionsRequiresServerURLAndToken covers request validation
// only — handleListPlexSections' actual success path needs a real Plex
// server to call, the same boundary as every other handler in this
// package backed by a live third-party client with no local mock
// injection point at this layer.
func TestListPlexSectionsRequiresServerURLAndToken(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", "/api/v1/settings/plex/sections", map[string]any{}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/settings/plex/sections", map[string]any{"serverUrl": "http://x"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/settings/plex/sections", map[string]any{"token": "t"}, nil), http.StatusBadRequest)
}
