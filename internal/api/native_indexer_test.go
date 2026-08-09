package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/librinode/librinode/internal/indexer"
)

type testNativeSearcher struct{}

func (testNativeSearcher) Search(context.Context, string, string) ([]indexer.Release, error) {
	return nil, nil
}
func (testNativeSearcher) Test(context.Context) error { return nil }

func init() {
	indexer.RegisterNative(indexer.NativeDef{
		Name:        "test-native",
		DisplayName: "Test Native",
		Protocol:    indexer.ProtocolTorrent,
		MediaTypes:  []string{"audiobook"},
		New:         func(*indexer.Indexer, *http.Client) indexer.Searcher { return testNativeSearcher{} },
	})
}

// TestNativeIndexerAddAndProwlarrExclusion: a native indexer needs no URL,
// appears in the app's own listing, is offered by the native-impls endpoint,
// but is hidden from Prowlarr (identified by its User-Agent) so it can't be
// treated as an indexer Prowlarr owns and prunes.
func TestNativeIndexerAddAndProwlarrExclusion(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	// It shows up in the list of selectable native implementations.
	var impls []map[string]any
	a.want(a.call("GET", "/api/v1/indexer/native", nil, &impls), http.StatusOK)
	found := false
	for _, im := range impls {
		if im["name"] == "test-native" {
			found = true
		}
	}
	if !found {
		t.Fatalf("native impls missing test-native: %+v", impls)
	}

	// Add one with no Newznab/Torznab URL — the native branch accepts it.
	a.want(a.call("POST", "/api/v1/indexer",
		map[string]any{"name": "My ABB", "type": "test-native", "enabled": true}, nil), http.StatusCreated)

	// The app's own UI (default UA) sees it.
	var uiList []map[string]any
	a.want(a.call("GET", "/api/v1/indexer", nil, &uiList), http.StatusOK)
	if !hasIndexerType(uiList, "test-native") {
		t.Errorf("app listing should include the native indexer: %+v", uiList)
	}

	// Prowlarr (its UA) must NOT see it.
	var prowlarrList []map[string]any
	a.want(a.callUA("Prowlarr/1.30.0", "GET", "/api/v1/indexer", nil, &prowlarrList), http.StatusOK)
	if hasIndexerType(prowlarrList, "test-native") {
		t.Errorf("native indexer leaked to Prowlarr: %+v", prowlarrList)
	}
}

func hasIndexerType(list []map[string]any, typ string) bool {
	for _, it := range list {
		if it["type"] == typ {
			return true
		}
	}
	return false
}

// TestDirectDownloadClientPersists: a direct-type client saves to the store
// (migration 017 dropped the type CHECK) and round-trips through the API.
func TestDirectDownloadClientPersists(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	dir := t.TempDir()
	var created map[string]any
	a.want(a.call("POST", "/api/v1/downloadclient",
		map[string]any{"name": "Fetcher", "type": "direct", "host": dir, "enabled": true},
		&created), http.StatusCreated)
	if created["type"] != "direct" {
		t.Fatalf("created = %+v", created)
	}

	var list []map[string]any
	a.want(a.call("GET", "/api/v1/downloadclient", nil, &list), http.StatusOK)
	found := false
	for _, c := range list {
		if c["type"] == "direct" && c["host"] == dir {
			found = true
		}
	}
	if !found {
		t.Errorf("direct client missing from list: %+v", list)
	}

	// Its connection test (folder writable) passes.
	a.want(a.call("POST", "/api/v1/downloadclient/test",
		map[string]any{"name": "Fetcher", "type": "direct", "host": dir}, nil), http.StatusOK)
}
