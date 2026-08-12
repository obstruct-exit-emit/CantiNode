package musicbrainz

import (
	"net/http"
	"strings"
	"testing"
)

const sampleArtistJSON = `{
	"id": "69158f97-4c07-4c4e-baf8-4e4ab1ed666e",
	"name": "Boards of Canada",
	"sort-name": "Boards of Canada",
	"release-groups": [
		{
			"id": "17d74d52-c92b-3b8d-9f87-218ab2d1c4a0",
			"title": "Music Has the Right to Children",
			"primary-type": "Album",
			"secondary-types": [],
			"first-release-date": "1998-04-20"
		},
		{
			"id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			"title": "Live at Warp",
			"primary-type": "Album",
			"secondary-types": ["Live"],
			"first-release-date": "2001-01-01"
		}
	],
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
	if gotInc != "release-groups+genres+tags+ratings" {
		t.Errorf("inc = %q, want release-groups+genres+tags+ratings", gotInc)
	}
	if artist.Name != "Boards of Canada" {
		t.Errorf("Name = %q", artist.Name)
	}
	if len(artist.ReleaseGroups) != 2 {
		t.Fatalf("len(ReleaseGroups) = %d, want 2", len(artist.ReleaseGroups))
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
	if artist.ReleaseGroups[0].PrimaryType != "Album" || artist.ReleaseGroups[0].FirstReleaseDate != "1998-04-20" {
		t.Errorf("ReleaseGroups[0] = %+v", artist.ReleaseGroups[0])
	}
	if len(artist.ReleaseGroups[1].SecondaryTypes) != 1 || artist.ReleaseGroups[1].SecondaryTypes[0] != "Live" {
		t.Errorf("ReleaseGroups[1].SecondaryTypes = %v, want [Live]", artist.ReleaseGroups[1].SecondaryTypes)
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
