// Package library holds LibriNode's library domain model — authors, series,
// books, and editions — and the SQLite persistence layer for it. Types carry
// JSON tags so API handlers can serialize them directly, *arr-style.
package library

// Edition formats. "physical" editions are tracked for completeness but are
// never grabbed.
const (
	FormatEbook     = "ebook"
	FormatAudiobook = "audiobook"
	FormatPhysical  = "physical"
	FormatUnknown   = "unknown"
)

type Author struct {
	ID          int64  `json:"id"`
	Source      string `json:"metadataSource"`
	ForeignID   string `json:"foreignAuthorId"`
	Name        string `json:"name"`
	SortName    string `json:"sortName"`
	Description string `json:"description"`
	ImageURL    string `json:"imageUrl"`
	Monitored   bool   `json:"monitored"`
	// Per-format library membership: the author was added to (or owns files
	// in) that library — independent of how many books are visible there.
	InEbookLibrary     bool `json:"inEbookLibrary"`
	InAudiobookLibrary bool `json:"inAudiobookLibrary"`
	// ProviderOverride pins this author to a metadata provider, beating the
	// global Settings → Metadata selection ("" = follow settings).
	ProviderOverride string `json:"providerOverride"`
	AddedAt          string `json:"addedAt"`
	UpdatedAt        string `json:"updatedAt"`
	// Grid stats, populated by library-scoped listings.
	BookCount  int `json:"bookCount"`
	OwnedCount int `json:"ownedCount"`
	// Populated on detail endpoints, not by ListAuthors.
	Books []Book `json:"books,omitempty"`
}

type Book struct {
	ID       int64  `json:"id"`
	AuthorID int64  `json:"authorId"`
	Source   string `json:"metadataSource"`
	// MediaType is "book" for prose (owned as ebook/audiobook), or
	// "manga"/"comic" for a series volume/issue.
	MediaType   string  `json:"mediaType"`
	ForeignID   string  `json:"foreignBookId"`
	Title       string  `json:"title"`
	SortTitle   string  `json:"sortTitle"`
	Description string  `json:"description"`
	ReleaseDate string  `json:"releaseDate"`
	Rating      float64 `json:"rating"`
	CoverURL    string  `json:"coverUrl"`
	Monitored   bool    `json:"monitored"`
	// Per-format library membership (prose books only): a book shows in
	// the Ebooks/Audiobooks library only when owned or deliberately added
	// there; each membership has its own monitored flag.
	InEbookLibrary     bool `json:"inEbookLibrary"`
	EbookMonitored     bool `json:"ebookMonitored"`
	InAudiobookLibrary bool `json:"inAudiobookLibrary"`
	AudiobookMonitored bool `json:"audiobookMonitored"`
	HasFile            bool `json:"hasFile"` // any media type
	HasEbookFile       bool `json:"hasEbookFile"`
	HasAudiobookFile   bool `json:"hasAudiobookFile"`
	// Per-variant ownership for manga volumes (colorized/monochrome share one
	// volume row); both false for non-manga.
	HasColorFile bool   `json:"hasColorFile"`
	HasMonoFile  bool   `json:"hasMonoFile"`
	AddedAt      string `json:"addedAt"`
	UpdatedAt    string `json:"updatedAt"`
	// Populated on detail endpoints.
	Editions []Edition    `json:"editions,omitempty"`
	Series   []SeriesLink `json:"series,omitempty"`
	Files    []BookFile   `json:"files,omitempty"`
}

type Edition struct {
	ID          int64  `json:"id"`
	BookID      int64  `json:"bookId"`
	Source      string `json:"metadataSource"`
	ForeignID   string `json:"foreignEditionId"`
	Title       string `json:"title"`
	ISBN13      string `json:"isbn13"`
	ASIN        string `json:"asin"`
	Format      string `json:"format"`
	Publisher   string `json:"publisher"`
	Language    string `json:"language"`
	ReleaseDate string `json:"releaseDate"`
	CoverURL    string `json:"coverUrl"`
	Monitored   bool   `json:"monitored"`
}

type Series struct {
	ID          int64  `json:"id"`
	Source      string `json:"metadataSource"`
	ForeignID   string `json:"foreignSeriesId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MediaType   string `json:"mediaType"` // book (prose series), manga, comic
	Monitored   bool   `json:"monitored"`
	MonitorNew  bool   `json:"monitorNew"` // future volumes start monitored
	// ProviderOverride pins this series to a metadata provider, beating the
	// global Settings → Metadata selection ("" = follow settings).
	ProviderOverride string `json:"providerOverride"`
	CoverURL         string `json:"coverUrl"`
	// Grid stats, populated by listings.
	ItemCount  int `json:"itemCount"`
	OwnedCount int `json:"ownedCount"`
	// Populated on detail endpoints.
	Volumes []Book `json:"volumes,omitempty"`
}

// SeriesLink is a book's membership in a series ("book 3 of Discworld").
type SeriesLink struct {
	SeriesID int64   `json:"seriesId"`
	Title    string  `json:"title"`
	Position float64 `json:"position"`
}
