package musicbrainz

// ArtistRef is a minimal MusicBrainz artist reference, as embedded in an
// artist-credit.
type ArtistRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	SortName string `json:"sort-name"`
}

// ArtistCredit is one entry of a recording or release's artist-credit —
// how the artist(s) are actually credited on that specific recording/
// release, which is not always identical to ArtistRef.Name (collaborations,
// "feat." credits, etc.). CantiNode only ever uses the first entry.
type ArtistCredit struct {
	Name   string    `json:"name"`
	Artist ArtistRef `json:"artist"`
}

// ReleaseGroup is the "abstract album" a Release belongs to — what
// MusicBrainz considers the same album across different pressings/
// editions/formats.
type ReleaseGroup struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	PrimaryType string `json:"primary-type"`
}

// Release is one specific pressing/edition of an album that a Recording
// appears on.
type Release struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Date         string       `json:"date"`
	ReleaseGroup ReleaseGroup `json:"release-group"`
}

// Recording is a MusicBrainz recording — the closest match to a single
// track file. Score is only populated by SearchRecordings (0-100, MusicBrainz's
// own relevance ranking); it's always 0 on a direct LookupRecording.
type Recording struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Length       int            `json:"length"` // milliseconds
	ArtistCredit []ArtistCredit `json:"artist-credit"`
	Releases     []Release      `json:"releases"`
	Score        int            `json:"score"`
}

// PrimaryArtist returns the recording's first credited artist, or a zero
// ArtistRef if it has none (shouldn't happen for a real MusicBrainz
// recording, but callers should still check ID != "").
func (r Recording) PrimaryArtist() ArtistRef {
	if len(r.ArtistCredit) == 0 {
		return ArtistRef{}
	}
	return r.ArtistCredit[0].Artist
}

// BestRelease returns the release CantiNode should treat as "the album"
// for this recording: preferredReleaseMBID if it's actually one of the
// recording's releases (the file's own tags already named a specific
// release), otherwise the first release MusicBrainz returned. Returns a
// zero Release if the recording has none linked at all — rare, but
// possible for a recording with no associated release.
func (r Recording) BestRelease(preferredReleaseMBID string) Release {
	if preferredReleaseMBID != "" {
		for _, rel := range r.Releases {
			if rel.ID == preferredReleaseMBID {
				return rel
			}
		}
	}
	if len(r.Releases) == 0 {
		return Release{}
	}
	return r.Releases[0]
}

type recordingSearchResponse struct {
	Recordings []Recording `json:"recordings"`
	Count      int         `json:"count"`
}

// ReleaseGroupSummary is one of an artist's release groups, as returned
// by LookupArtist — enough to decide whether internal/acquisition should
// want it (PrimaryType == "Album", no secondary types like Live/
// Compilation) without a further lookup.
type ReleaseGroupSummary struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
}

// Artist is a MusicBrainz artist, with its release groups (when fetched
// via LookupArtist's inc=release-groups). Score is only populated by
// SearchArtists (0-100, MusicBrainz's own relevance ranking) — always 0
// on a direct LookupArtist, same convention as Recording.Score.
type Artist struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	SortName      string                `json:"sort-name"`
	ReleaseGroups []ReleaseGroupSummary `json:"release-groups"`
	Score         int                   `json:"score"`
}

type artistSearchResponse struct {
	Artists []Artist `json:"artists"`
	Count   int      `json:"count"`
}

// ReleaseSearchResult is one candidate from SearchReleases — enough to
// rank candidates (MusicBrainz's own relevance Score, plus TrackCount to
// cross-check against how many files are actually in the folder being
// matched) without paying for a full tracklist fetch on every candidate,
// only on whichever single one the caller ends up picking (see
// internal/scanner's folder-level matching).
type ReleaseSearchResult struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	ArtistCredit []ArtistCredit `json:"artist-credit"`
	ReleaseGroup ReleaseGroup   `json:"release-group"`
	TrackCount   int            `json:"track-count"` // aggregate across every medium
	Score        int            `json:"score"`
}

type releaseSearchResponse struct {
	Releases []ReleaseSearchResult `json:"releases"`
	Count    int                   `json:"count"`
}

// ReleaseTrack is one track position within a release's medium, as
// returned by LookupReleaseWithTracklist — distinct from a bare Recording:
// it carries this specific release's own track/disc position and title,
// which a bare Recording lookup never does (see scanner.applyMatch's doc
// comment on where track/disc number normally come from).
type ReleaseTrack struct {
	Position int `json:"position"` // 1-based position within its medium — compared against a file's own TrackNumber tag, and what's ultimately stored
	// Number is MusicBrainz's own display label; not always numeric (e.g.
	// "A1" on a vinyl release), so Position — not Number — is what
	// CantiNode actually compares/stores.
	Number    string    `json:"number"`
	Title     string    `json:"title"`
	Length    int       `json:"length"` // milliseconds, this release's own timing (can differ slightly from Recording.Length)
	Recording Recording `json:"recording"`
}

// ReleaseMedium is one disc/side of a release, with its own tracklist.
type ReleaseMedium struct {
	Format     string         `json:"format"`
	Position   int            `json:"position"` // disc number
	TrackCount int            `json:"track-count"`
	Tracks     []ReleaseTrack `json:"tracks"`
}

// ReleaseWithTracklist is a release fetched via LookupReleaseWithTracklist
// (inc=recordings+artist-credits+release-groups) — same identity as
// Release, plus its full medium/track breakdown. Used by
// internal/scanner's folder-level matching to slot every local file in a
// folder into a specific disc/track position within one chosen release.
type ReleaseWithTracklist struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Date         string          `json:"date"`
	ArtistCredit []ArtistCredit  `json:"artist-credit"`
	ReleaseGroup ReleaseGroup    `json:"release-group"`
	Media        []ReleaseMedium `json:"media"`
}

// PrimaryArtist mirrors Recording.PrimaryArtist for a release.
func (r ReleaseWithTracklist) PrimaryArtist() ArtistRef {
	if len(r.ArtistCredit) == 0 {
		return ArtistRef{}
	}
	return r.ArtistCredit[0].Artist
}

// AsRelease returns r's plain Release view — same identity fields, no
// tracklist — so it can be dropped straight into a synthesized
// Recording.Releases and reused through applyMatch's existing
// BestRelease-based plumbing unchanged (see
// scanner.recordingForReleaseTrack).
func (r ReleaseWithTracklist) AsRelease() Release {
	return Release{ID: r.ID, Title: r.Title, Date: r.Date, ReleaseGroup: r.ReleaseGroup}
}
