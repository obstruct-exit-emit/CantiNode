package musicbrainz

import (
	"fmt"
	"strings"
)

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
	// SecondaryTypes flags rg as something other than a plain studio
	// album despite PrimaryType == "Album" — "Compilation", "Live",
	// "Soundtrack", etc. Populated on both LookupRecording (inc=
	// release-groups) and SearchRecordings (included by default) without
	// any further request — see isCleanAlbum, used by BestRelease to
	// avoid preferring, say, a box-set rerelease over the actual studio
	// album a recording also happens to appear on.
	SecondaryTypes []string `json:"secondary-types"`
}

// isCleanAlbum reports whether rg is MusicBrainz's convention for an
// ordinary studio album — PrimaryType "Album" with no SecondaryTypes — as
// opposed to a compilation/live/soundtrack/etc. release that happens to
// reuse one of its recordings (e.g. a "best of" box set, or a live
// recording of the same song). Used by BestRelease to break ties among a
// recording's releases when no specific release was requested.
func (rg ReleaseGroup) isCleanAlbum() bool {
	return rg.PrimaryType == "Album" && len(rg.SecondaryTypes) == 0
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
// release), otherwise the first release belonging to a "clean" studio
// album (see ReleaseGroup.isCleanAlbum). Recordings are frequently reused
// across unrelated releases — a compilation, a box set, a live album —
// and MusicBrainz's own Releases ordering has no preference for the
// studio album over any of those; without this, a track whose recording
// happens to also appear on, say, a box set can resolve to that box set
// instead of the actual album a scanned folder represents. Falls back to
// the first release of any kind if the recording has no clean album among
// its releases (fine for a recording that genuinely only exists on a
// compilation/live release). Returns a zero Release if the recording has
// none linked at all — rare, but possible for a recording with no
// associated release.
func (r Recording) BestRelease(preferredReleaseMBID string) Release {
	if preferredReleaseMBID != "" {
		for _, rel := range r.Releases {
			if rel.ID == preferredReleaseMBID {
				return rel
			}
		}
	}
	for _, rel := range r.Releases {
		if rel.ReleaseGroup.isCleanAlbum() {
			return rel
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

// ReleaseGroupSummary is one of an artist's release groups, as returned by
// BrowseArtistReleaseGroups — enough to decide whether internal/acquisition
// should want it (PrimaryType == "Album", no secondary types like Live/
// Compilation) without a further lookup.
type ReleaseGroupSummary struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
}

// releaseGroupBrowseResponse is one page of BrowseArtistReleaseGroups —
// Count is the artist's TRUE total release-group count (not just this
// page's length), needed to know when every page has been fetched.
type releaseGroupBrowseResponse struct {
	ReleaseGroups []ReleaseGroupSummary `json:"release-groups"`
	Count         int                   `json:"release-group-count"`
	Offset        int                   `json:"release-group-offset"`
}

// Genre is one of MusicBrainz's own curated genre tags (inc=genres) —
// distinct from Tag, which is free-form community folksonomy.
type Genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Tag is a free-form community folksonomy tag (inc=tags).
type Tag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Rating is MusicBrainz's community rating (inc=ratings) — Value is 0-5
// (possibly fractional, zero when nobody's rated it yet), VotesCount how
// many votes it's based on.
type Rating struct {
	Value      float64 `json:"value"`
	VotesCount int     `json:"votes-count"`
}

// Artist is a MusicBrainz artist. Score is only populated by SearchArtists
// (0-100, MusicBrainz's own relevance ranking) — always 0 on a direct
// LookupArtist, same convention as Recording.Score. Release groups are
// deliberately NOT part of this type — LookupArtist's own inc=release-groups
// sub-resource is silently capped at MusicBrainz's default page size (25),
// which truncated every artist's discography before this comment was
// written; BrowseArtistReleaseGroups is the real, fully-paginated way to
// get an artist's complete release-group list.
//
// Genres/Tags/Rating are only populated when LookupArtist's inc includes
// genres+tags+ratings (see client.go) — cached by internal/api even though
// nothing displays them yet, so a future feature never needs a fresh
// MusicBrainz round trip for data already fetched once.
type Artist struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	SortName string  `json:"sort-name"`
	Genres   []Genre `json:"genres"`
	Tags     []Tag   `json:"tags"`
	Rating   Rating  `json:"rating"`
	Score    int     `json:"score"`
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
	ID    string `json:"id"`
	Title string `json:"title"`
	Date  string `json:"date"`
	// Status is MusicBrainz's release status ("Official", "Promotion",
	// "Bootleg", "Pseudo-Release") — populated by both a search and a
	// browse-by-release-group request. Used to prefer an official release
	// as the representative tracklist for a release group CantiNode
	// doesn't own yet (see pickRepresentativeRelease).
	Status         string         `json:"status"`
	Country        string         `json:"country"`
	Disambiguation string         `json:"disambiguation"`
	ArtistCredit   []ArtistCredit `json:"artist-credit"`
	ReleaseGroup   ReleaseGroup   `json:"release-group"`
	TrackCount     int            `json:"track-count"` // aggregate across every medium
	// Media is populated when the request includes inc=media (see
	// BrowseReleaseGroupReleases) — enough to show/compare an edition's
	// disc layout (e.g. "2×CD") without paying for a full tracklist fetch.
	Media []ReleaseMediumSummary `json:"media"`
	Score int                    `json:"score"`
}

// ReleaseMediumSummary is one disc/side of a release, as returned by
// inc=media — format and track count only, no actual tracklist. Distinct
// from ReleaseMedium (LookupReleaseWithTracklist's richer per-track view).
type ReleaseMediumSummary struct {
	Format     string `json:"format"`
	TrackCount int    `json:"track-count"`
}

// TotalTrackCount returns r's total track count across every medium —
// from Media when populated (inc=media), falling back to the flat
// TrackCount field a search response carries instead.
func (r ReleaseSearchResult) TotalTrackCount() int {
	if len(r.Media) == 0 {
		return r.TrackCount
	}
	total := 0
	for _, m := range r.Media {
		total += m.TrackCount
	}
	return total
}

// MediaSummary renders r.Media as a short human label ("2×CD", "Digital
// Media", "CD + DVD") for a version picker — empty when Media wasn't
// requested/populated.
func (r ReleaseSearchResult) MediaSummary() string {
	if len(r.Media) == 0 {
		return ""
	}
	counts := map[string]int{}
	var order []string
	for _, m := range r.Media {
		format := m.Format
		if format == "" {
			format = "Unknown"
		}
		if counts[format] == 0 {
			order = append(order, format)
		}
		counts[format]++
	}
	var parts []string
	for _, format := range order {
		n := counts[format]
		if n > 1 {
			parts = append(parts, fmt.Sprintf("%d×%s", n, format))
		} else {
			parts = append(parts, format)
		}
	}
	return strings.Join(parts, " + ")
}

type releaseSearchResponse struct {
	Releases []ReleaseSearchResult `json:"releases"`
	Count    int                   `json:"count"`
}

// releaseBrowseResponse is one page of BrowseReleaseGroupReleases — a
// browse-by-relation response, whose pagination fields are named
// differently from a full-text search response's (release-count/
// release-offset here, vs releaseSearchResponse's plain "count" for
// SearchReleases) — the same distinction releaseGroupBrowseResponse
// already makes for BrowseArtistReleaseGroups.
type releaseBrowseResponse struct {
	Releases []ReleaseSearchResult `json:"releases"`
	Count    int                   `json:"release-count"`
	Offset   int                   `json:"release-offset"`
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
