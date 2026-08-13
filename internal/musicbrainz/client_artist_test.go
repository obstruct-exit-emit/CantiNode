package musicbrainz

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const sampleArtistJSON = `{
	"id": "69158f97-4c07-4c4e-baf8-4e4ab1ed666e",
	"name": "Boards of Canada",
	"sort-name": "Boards of Canada",
	"genres": [{"name": "idm", "count": 12}],
	"tags": [{"name": "scottish", "count": 3}],
	"rating": {"value": 4.5, "votes-count": 42}
}`

func TestLookupArtist(t *testing.T) {
	var gotPath, gotInc string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInc = r.URL.Query().Get("inc")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistJSON))
	})

	artist, err := c.LookupArtist(t.Context(), "69158f97-4c07-4c4e-baf8-4e4ab1ed666e")
	if err != nil {
		t.Fatalf("LookupArtist: %v", err)
	}
	if gotPath != "/artist/69158f97-4c07-4c4e-baf8-4e4ab1ed666e" {
		t.Errorf("path = %q", gotPath)
	}
	if gotInc != "genres+tags+ratings" {
		t.Errorf("inc = %q, want genres+tags+ratings", gotInc)
	}
	if artist.Name != "Boards of Canada" {
		t.Errorf("Name = %q", artist.Name)
	}
	if len(artist.Genres) != 1 || artist.Genres[0].Name != "idm" {
		t.Errorf("Genres = %+v, want [idm]", artist.Genres)
	}
	if len(artist.Tags) != 1 || artist.Tags[0].Name != "scottish" {
		t.Errorf("Tags = %+v, want [scottish]", artist.Tags)
	}
	if artist.Rating.Value != 4.5 || artist.Rating.VotesCount != 42 {
		t.Errorf("Rating = %+v, want {4.5 42}", artist.Rating)
	}
}

// TestBrowseArtistReleaseGroupsSinglePage covers the common case: an
// artist whose entire discography fits in one page (fewer than 100 release
// groups) — the loop should make exactly one request and stop, since the
// accumulated count already meets the reported total.
func TestBrowseArtistReleaseGroupsSinglePage(t *testing.T) {
	var requests int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"release-group-count": 2,
			"release-group-offset": 0,
			"release-groups": [
				{"id": "17d74d52-c92b-3b8d-9f87-218ab2d1c4a0", "title": "Music Has the Right to Children", "primary-type": "Album", "secondary-types": [], "first-release-date": "1998-04-20"},
				{"id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "title": "Live at Warp", "primary-type": "Album", "secondary-types": ["Live"], "first-release-date": "2001-01-01"}
			]
		}`))
	})

	groups, err := c.BrowseArtistReleaseGroups(t.Context(), "69158f97-4c07-4c4e-baf8-4e4ab1ed666e")
	if err != nil {
		t.Fatalf("BrowseArtistReleaseGroups: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want exactly 1 (single page)", requests)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}
	if groups[0].PrimaryType != "Album" || groups[0].FirstReleaseDate != "1998-04-20" {
		t.Errorf("groups[0] = %+v", groups[0])
	}
	if len(groups[1].SecondaryTypes) != 1 || groups[1].SecondaryTypes[0] != "Live" {
		t.Errorf("groups[1].SecondaryTypes = %v, want [Live]", groups[1].SecondaryTypes)
	}
}

// TestBrowseArtistReleaseGroupsPaginatesFully is the regression test for
// the real bug this method fixes: an artist with more release groups than
// fit on one page (previously silently truncated to the first 25 by
// LookupArtist's own inc=release-groups) must have every page fetched and
// combined into one complete list.
func TestBrowseArtistReleaseGroupsPaginatesFully(t *testing.T) {
	const total = 130 // more than one page (100) but less than two full pages
	var gotOffsets []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		gotOffsets = append(gotOffsets, offset)
		start := 0
		fmt.Sscanf(offset, "%d", &start)
		limit := 100
		end := start + limit
		if end > total {
			end = total
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf(`{"release-group-count": %d, "release-group-offset": %d, "release-groups": [`, total, start))
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`{"id": "rg-%d", "title": "Album %d", "primary-type": "Album", "secondary-types": [], "first-release-date": "2000-01-01"}`, i, i))
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sb.String()))
	})

	groups, err := c.BrowseArtistReleaseGroups(t.Context(), "prolific-artist-mbid")
	if err != nil {
		t.Fatalf("BrowseArtistReleaseGroups: %v", err)
	}
	if len(groups) != total {
		t.Fatalf("len(groups) = %d, want %d", len(groups), total)
	}
	if len(gotOffsets) != 2 {
		t.Fatalf("made %d requests (offsets %v), want exactly 2 pages", len(gotOffsets), gotOffsets)
	}
	if gotOffsets[0] != "0" || gotOffsets[1] != "100" {
		t.Errorf("offsets = %v, want [0 100]", gotOffsets)
	}
	// Confirm no duplicate/missing IDs across the page boundary.
	seen := map[string]bool{}
	for _, g := range groups {
		if seen[g.ID] {
			t.Errorf("duplicate release group %s across pages", g.ID)
		}
		seen[g.ID] = true
	}
}

const sampleArtistSearchJSON = `{
	"count": 1,
	"artists": [
		{
			"id": "69158f97-4c07-4c4e-baf8-4e4ab1ed666e",
			"name": "Boards of Canada",
			"sort-name": "Boards of Canada",
			"score": 100
		}
	]
}`

func TestSearchArtists(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleArtistSearchJSON))
	})

	artists, err := c.SearchArtists(t.Context(), "Boards of Canada")
	if err != nil {
		t.Fatalf("SearchArtists: %v", err)
	}
	if !strings.Contains(gotQuery, `artist:"Boards of Canada"`) {
		t.Errorf("query = %q", gotQuery)
	}
	if len(artists) != 1 || artists[0].Score != 100 {
		t.Errorf("artists = %+v", artists)
	}
}

func TestSearchArtistsRequiresName(t *testing.T) {
	c := NewClient("0.1.0-test", "")
	if _, err := c.SearchArtists(t.Context(), ""); err == nil {
		t.Error("expected an error for an empty name")
	}
}
