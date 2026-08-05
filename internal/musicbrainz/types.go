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
