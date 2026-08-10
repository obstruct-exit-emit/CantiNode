package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/cantinode/cantinode/internal/indexer"
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
		MediaTypes:  []string{"music"},
		New:         func(*indexer.Indexer, *http.Client) indexer.Searcher { return testNativeSearcher{} },
	})
}

// TestNativeIndexerAddAndList: a native indexer needs no Newznab/Torznab
// URL, appears in the app's own listing, and is offered by the native-impls
// endpoint.
func TestNativeIndexerAddAndList(t *testing.T) {
	a := newTestAPI(t)

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

	var list []map[string]any
	a.want(a.call("GET", "/api/v1/indexer", nil, &list), http.StatusOK)
	if !hasIndexerType(list, "test-native") {
		t.Errorf("listing should include the native indexer: %+v", list)
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
	a := newTestAPI(t)

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
