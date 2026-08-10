package indexer

import (
	"context"
	"net/http"
	"sort"
)

// Native indexers are built-in Go sources with no Newznab/Torznab endpoint
// of their own — a bespoke API (Prowlarr's own search, queried directly
// rather than through a Newznab-shaped proxy) or a scraped/bespoke site.
// Each registered implementation is selectable as an indexer "type" (its
// registry name is stored in the same `type` column as newznab/torznab)
// and configured in-app. A native searcher feeds the very same SearchAll
// merge, scoring, and grab pipeline as the API clients: it returns
// ordinary Releases (a torrent source hands back a magnet, which rides the
// existing qBittorrent path untouched).
//
// A scraped source specifically is dual-use: nothing like that is bundled
// or enabled out of the box — a user would add one deliberately and be
// responsible for its use.

// Searcher is one native source's capability.
type Searcher interface {
	// Search returns candidate releases for a free-text query in a media type
	// the source serves; a media type it doesn't serve yields (nil, nil).
	Search(ctx context.Context, query, mediaType string) ([]Release, error)
	// Test verifies the source is reachable and usable (a light request).
	Test(ctx context.Context) error
}

// Resolver is an optional capability: a source whose search results carry a
// stand-in download URL (e.g. a release page) that must be turned into the real
// downloadable URL only at grab time. Deferring this keeps search cheap — one
// request instead of one per result — which is what keeps a scraped source from
// tripping rate limits and IP bans. A source that lists a release page in
// search results and resolves it to a magnet only at grab time would use it.
type Resolver interface {
	// Resolve turns a search result's download URL into the real one (a magnet,
	// a file URL, ...). It is called for exactly the release the user grabs.
	Resolve(ctx context.Context, downloadURL string) (string, error)
}

// NativeDef describes a registered native implementation.
type NativeDef struct {
	Name        string   // registry key AND the stored indexer type
	DisplayName string   // human label for the Settings dropdown
	Protocol    string   // ProtocolTorrent | ProtocolUsenet
	MediaTypes  []string // media types served; empty means all
	// DefaultBaseURL is a starting site URL for sources whose domain rotates
	// (the user may override it on the indexer); "" means the source has no
	// built-in default — see NeedsBaseURL for whether one must be entered.
	DefaultBaseURL string
	// NeedsBaseURL marks sources with no default of their own that still
	// require a URL (e.g. Prowlarr — every instance lives at an address only
	// its own user knows, unlike a scraped site with a well-known domain).
	NeedsBaseURL bool
	// NeedsAPIKey marks sources that require a key (e.g. a membership token).
	NeedsAPIKey bool
	// WIP flags an experimental source — scraped sites that are fragile and
	// need more work; the UI surfaces a "work in progress" warning so a user
	// knows what they're enabling.
	WIP bool
	// New builds a searcher from the stored indexer config and a shared client.
	New func(ind *Indexer, httpc *http.Client) Searcher
}

// Serves reports whether the source handles a media type.
func (d NativeDef) Serves(mediaType string) bool {
	if len(d.MediaTypes) == 0 {
		return true
	}
	for _, mt := range d.MediaTypes {
		if mt == mediaType {
			return true
		}
	}
	return false
}

var nativeRegistry = map[string]NativeDef{}

// RegisterNative makes a native implementation available under def.Name.
// Registering the same name twice panics — a wiring bug, caught at startup.
func RegisterNative(def NativeDef) {
	if def.Name == "" || def.New == nil {
		panic("indexer: native def needs a Name and New")
	}
	if _, dup := nativeRegistry[def.Name]; dup {
		panic("indexer: native implementation already registered: " + def.Name)
	}
	nativeRegistry[def.Name] = def
}

// NativeDefFor returns the def registered under a type name, if any.
func NativeDefFor(typ string) (NativeDef, bool) {
	d, ok := nativeRegistry[typ]
	return d, ok
}

// IsNativeType reports whether a type name is a registered native impl.
func IsNativeType(typ string) bool {
	_, ok := nativeRegistry[typ]
	return ok
}

// NativeImplementations lists registered impls, sorted by name — for the
// Settings UI to offer as indexer types.
func NativeImplementations() []NativeDef {
	defs := make([]NativeDef, 0, len(nativeRegistry))
	for _, d := range nativeRegistry {
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}
