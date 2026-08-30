package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/cantinode/cantinode/internal/importlist"
)

// TestAddImportListValidatesPerType covers request validation only — same
// boundary as TestQuickAddMusicArtistRequiresMbid (music_test.go): CRUD
// add/update/list/delete never touch MusicBrainz/Last.fm themselves, but
// handleTestImportList does, so it's left untested here for the same
// reason (a real, network-reaching client with no local mock injection
// point at this layer).
func TestAddImportListValidatesPerType(t *testing.T) {
	a := newTestAPI(t)
	a.want(a.call("POST", "/api/v1/importlist", map[string]any{}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist", map[string]any{"name": "x"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist", map[string]any{"name": "x", "type": "bogus"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist",
		map[string]any{"name": "x", "type": "musicbrainz_series"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist",
		map[string]any{"name": "x", "type": "list"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist",
		map[string]any{"name": "x", "type": "list", "sourceUrl": "not-a-url"}, nil), http.StatusBadRequest)
	a.want(a.call("POST", "/api/v1/importlist",
		map[string]any{"name": "x", "type": "lastfm"}, nil), http.StatusBadRequest)
}

// TestImportListCRUD exercises add/list/update/delete for a "list"-type
// entry — the one type whose add/update path needs no MusicBrainz/Last.fm
// request at all, so it's safe to run against the real client newTestAPI
// wires up (see the package doc comment on why this layer has no mock
// injection point).
func TestImportListCRUD(t *testing.T) {
	a := newTestAPI(t)

	var created importlist.ImportList
	a.want(a.call("POST", "/api/v1/importlist", map[string]any{
		"name": "My Artists", "type": "list", "listText": "Boards of Canada\nAphex Twin",
	}, &created), http.StatusCreated)
	if created.ID == 0 || created.LastfmKind != "user" {
		t.Errorf("created = %+v, want a nonzero id and lastfmKind defaulted to \"user\"", created)
	}

	var listed []importlist.ImportList
	a.want(a.call("GET", "/api/v1/importlist", nil, &listed), http.StatusOK)
	if len(listed) != 1 || listed[0].Name != "My Artists" {
		t.Fatalf("listed = %+v", listed)
	}

	var updated importlist.ImportList
	a.want(a.call("PUT", fmt.Sprintf("/api/v1/importlist/%d", created.ID), map[string]any{
		"name": "My Artists", "type": "list", "listText": "Boards of Canada", "enabled": false,
	}, &updated), http.StatusOK)
	if updated.Enabled {
		t.Errorf("updated.Enabled = true, want false")
	}

	a.want(a.call("DELETE", fmt.Sprintf("/api/v1/importlist/%d", created.ID), nil, nil), http.StatusNoContent)
}
