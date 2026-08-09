package indexer

import (
	"context"
	"net/http"
	"testing"
)

type fakeSearcher struct {
	results []Release
	tested  bool
}

func (f *fakeSearcher) Search(_ context.Context, _, _ string) ([]Release, error) {
	return f.results, nil
}
func (f *fakeSearcher) Test(_ context.Context) error { f.tested = true; return nil }

func TestNativeRegistryAndDispatch(t *testing.T) {
	fake := &fakeSearcher{results: []Release{{Title: "A Result"}}}
	RegisterNative(NativeDef{
		Name:        "faketorrent",
		DisplayName: "Fake Torrent",
		Protocol:    ProtocolTorrent,
		MediaTypes:  []string{"audiobook"},
		New:         func(_ *Indexer, _ *http.Client) Searcher { return fake },
	})

	if !IsNativeType("faketorrent") {
		t.Fatal("faketorrent should be registered")
	}
	if IsNativeType("torznab") {
		t.Error("torznab is not a native type")
	}
	if defs := NativeImplementations(); len(defs) == 0 || defs[0].Name != "faketorrent" {
		t.Errorf("NativeImplementations = %+v", defs)
	}

	// Protocol comes from the registered def.
	ind := &Indexer{Type: "faketorrent", Name: "Fake"}
	if got := ind.Protocol(); got != ProtocolTorrent {
		t.Errorf("native Protocol() = %q, want torrent", got)
	}

	svc := &Service{client: NewClient()}

	// Dispatches to the native searcher for a served media type.
	got, err := svc.searchOne(context.Background(), ind, "q", "q", "audiobook")
	if err != nil || len(got) != 1 || got[0].Title != "A Result" {
		t.Fatalf("searchOne(audiobook) = %+v, %v", got, err)
	}
	// A media type it doesn't serve yields nothing, not an error.
	got, err = svc.searchOne(context.Background(), ind, "q", "q", "ebook")
	if err != nil || got != nil {
		t.Errorf("searchOne(ebook) = %+v, %v; want nil, nil", got, err)
	}
	// Test dispatches to the native searcher too.
	if err := svc.Test(context.Background(), ind); err != nil {
		t.Errorf("Test: %v", err)
	}
	if !fake.tested {
		t.Error("Service.Test should have reached the native searcher")
	}
}

func TestRegisterNativeRejectsBadDefs(t *testing.T) {
	assertPanics := func(name string, def NativeDef) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic", name)
			}
		}()
		RegisterNative(def)
	}
	assertPanics("no name", NativeDef{New: func(_ *Indexer, _ *http.Client) Searcher { return nil }})
	assertPanics("no constructor", NativeDef{Name: "x"})
}
