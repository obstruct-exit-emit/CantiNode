// Package indexer implements CantiNode's indexer framework: Newznab (usenet)
// and Torznab (torrent) clients behind one API, indexer configuration
// storage, and aggregated release search. Release scoring and automatic
// grabbing (internal/release, internal/autosearch) build on top of this.
package indexer

// Indexer types. Torznab is Newznab's API shape served by torrent indexers
// (Jackett/Prowlarr style) with torrent-specific attributes on results.
const (
	TypeNewznab = "newznab"
	TypeTorznab = "torznab"
)

// Protocols derived from the indexer type. Direct releases are plain HTTP
// file links (possibly a "|"-separated mirror list), downloaded by the
// CantiNode-side direct download client rather than an external program.
const (
	ProtocolUsenet  = "usenet"
	ProtocolTorrent = "torrent"
	ProtocolDirect  = "direct"
	// ProtocolMixed marks a source (like Prowlarr) that aggregates both
	// torrent and usenet indexers — its own NativeDef.Protocol is purely
	// informational (shown in the Settings "add indexer" dropdown); each
	// of its actual search results carries its own real Release.Protocol.
	ProtocolMixed = "mixed"
)

// Indexer is one configured indexer endpoint.
type Indexer struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	// AudioCategories are the comma-separated Newznab/Torznab category ids
	// used for music searches (3010 = Audio/MP3, 3040 = Audio/Lossless).
	AudioCategories string `json:"audioCategories"`
	Enabled         bool   `json:"enabled"`
	Priority        int    `json:"priority"` // 1-50, lower wins ties
	AddedAt         string `json:"addedAt"`
}

// Protocol reports how releases from this indexer are downloaded. A native
// source's protocol comes from its registered definition.
func (i *Indexer) Protocol() string {
	if def, ok := NativeDefFor(i.Type); ok {
		return def.Protocol
	}
	if i.Type == TypeTorznab {
		return ProtocolTorrent
	}
	return ProtocolUsenet
}

// CategoriesFor picks the category list for a media type's searches — only
// music exists now, so this always returns AudioCategories; the mediaType
// parameter survives for API-shape stability with SearchAll's callers.
func (i *Indexer) CategoriesFor(mediaType string) string {
	return i.AudioCategories
}

// Release is one search result from an indexer — a candidate file for a
// wanted book. Scoring and mapping to library books happen in later slices.
type Release struct {
	IndexerID   int64  `json:"indexerId"`
	Indexer     string `json:"indexer"`
	Protocol    string `json:"protocol"`
	Title       string `json:"title"`
	GUID        string `json:"guid"`
	InfoURL     string `json:"infoUrl,omitempty"`
	DownloadURL string `json:"downloadUrl"`
	Size        int64  `json:"size"`
	PublishDate string `json:"publishDate,omitempty"`
	Categories  []int  `json:"categories,omitempty"`
	// Keywords is extra searchable text a native source can supply beyond
	// Title — e.g. a per-post tag list, which often names the author even
	// when the title itself doesn't. Scoring's author check falls back to
	// it; it's never shown to the user and never used for the book-title
	// check (a stray keyword match there would be too easy to fool).
	Keywords string `json:"-"`
	// Torrent-only; -1 means unknown/not applicable (usenet).
	Seeders int `json:"seeders"`
	Peers   int `json:"peers"`
}
