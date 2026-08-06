package musicbrainz

import (
	"net/http"
	"strings"
	"testing"
)

const sampleReleaseSearchJSON = `{
	"count": 1,
	"releases": [
		{
			"id": "7e72ce86-045d-483b-a136-2a2fd676d38c",
			"title": "Layla and Other Assorted Love Songs",
			"date": "2011",
			"score": 100,
			"track-count": 14,
			"artist-credit": [
				{
					"name": "Derek and the Dominos",
					"artist": {
						"id": "2155a81a-f0c6-417a-9b16-2f86f98bb8bc",
						"name": "Derek and the Dominos",
						"sort-name": "Derek and the Dominos"
					}
				}
			],
			"release-group": {
				"id": "ba53dcbd-9328-3cf7-bc72-179b4512867e",
				"title": "Layla and Other Assorted Love Songs",
				"primary-type": "Album"
			}
		}
	]
}`

func TestSearchReleases(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleReleaseSearchJSON))
	})

	results, err := c.SearchReleases(t.Context(), "Derek and the Dominos", "Layla and Other Assorted Love Songs")
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Score != 100 {
		t.Errorf("Score = %d, want 100", results[0].Score)
	}
	if results[0].TrackCount != 14 {
		t.Errorf("TrackCount = %d, want 14", results[0].TrackCount)
	}
	if results[0].ReleaseGroup.ID != "ba53dcbd-9328-3cf7-bc72-179b4512867e" {
		t.Errorf("ReleaseGroup.ID = %q", results[0].ReleaseGroup.ID)
	}
	for _, want := range []string{`artist:"Derek and the Dominos"`, `release:"Layla and Other Assorted Love Songs"`} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q does not contain %q", gotQuery, want)
		}
	}
}

func TestSearchReleasesRequiresAtLeastOneField(t *testing.T) {
	c := NewClient("0.1.0-test", "")
	if _, err := c.SearchReleases(t.Context(), "", ""); err == nil {
		t.Error("expected an error when artist and release are both empty")
	}
}

func TestSearchReleasesSanitizesReleaseTitle(t *testing.T) {
	var gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"count":0,"releases":[]}`))
	})

	if _, err := c.SearchReleases(t.Context(), "Derek and the Dominos", "Layla and Other Assorted Love Songs SHM-CD"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotQuery, "SHM") {
		t.Errorf("query %q still contains rip-format junk, want it sanitized", gotQuery)
	}
}

// sampleReleaseWithTracklistJSON is a trimmed two-track fixture, field
// shapes confirmed against the real MusicBrainz API for this exact
// release (Derek and the Dominos - Layla and Other Assorted Love Songs).
const sampleReleaseWithTracklistJSON = `{
	"id": "7e72ce86-045d-483b-a136-2a2fd676d38c",
	"title": "Layla and Other Assorted Love Songs",
	"date": "2011",
	"artist-credit": [
		{
			"name": "Derek and the Dominos",
			"artist": {
				"id": "2155a81a-f0c6-417a-9b16-2f86f98bb8bc",
				"name": "Derek and the Dominos",
				"sort-name": "Derek and the Dominos"
			}
		}
	],
	"release-group": {
		"id": "ba53dcbd-9328-3cf7-bc72-179b4512867e",
		"title": "Layla and Other Assorted Love Songs",
		"primary-type": "Album"
	},
	"media": [
		{
			"format": "CD",
			"position": 1,
			"track-count": 2,
			"tracks": [
				{
					"position": 1,
					"number": "1",
					"title": "I Looked Away",
					"length": 187240,
					"recording": {
						"id": "e9aea3cf-0b24-4f6d-89f1-990446b0b739",
						"title": "I Looked Away",
						"length": 186533
					}
				},
				{
					"position": 2,
					"number": "2",
					"title": "Bell Bottom Blues",
					"length": 303533,
					"recording": {
						"id": "c0ccb9e4-7627-4683-89d5-3d89a117e3e6",
						"title": "Bell Bottom Blues",
						"length": 303533
					}
				}
			]
		}
	]
}`

func TestLookupReleaseWithTracklist(t *testing.T) {
	var gotPath, gotInc string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInc = r.URL.Query().Get("inc")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sampleReleaseWithTracklistJSON))
	})

	rel, err := c.LookupReleaseWithTracklist(t.Context(), "7e72ce86-045d-483b-a136-2a2fd676d38c")
	if err != nil {
		t.Fatalf("LookupReleaseWithTracklist: %v", err)
	}

	if gotPath != "/release/7e72ce86-045d-483b-a136-2a2fd676d38c" {
		t.Errorf("request path = %q", gotPath)
	}
	if !strings.Contains(gotInc, "recordings") {
		t.Errorf("inc = %q, want it to request recordings", gotInc)
	}

	if len(rel.Media) != 1 {
		t.Fatalf("len(Media) = %d, want 1", len(rel.Media))
	}
	medium := rel.Media[0]
	if medium.Position != 1 || medium.TrackCount != 2 || medium.Format != "CD" {
		t.Errorf("medium = %+v", medium)
	}
	if len(medium.Tracks) != 2 {
		t.Fatalf("len(Tracks) = %d, want 2", len(medium.Tracks))
	}
	if medium.Tracks[0].Position != 1 || medium.Tracks[0].Title != "I Looked Away" {
		t.Errorf("track[0] = %+v", medium.Tracks[0])
	}
	if medium.Tracks[0].Recording.ID != "e9aea3cf-0b24-4f6d-89f1-990446b0b739" {
		t.Errorf("track[0].Recording.ID = %q", medium.Tracks[0].Recording.ID)
	}
	if medium.Tracks[0].Recording.Length != 186533 {
		t.Errorf("track[0].Recording.Length = %d, want the recording's own (not the release track's) length 186533", medium.Tracks[0].Recording.Length)
	}
	if rel.PrimaryArtist().Name != "Derek and the Dominos" {
		t.Errorf("PrimaryArtist().Name = %q", rel.PrimaryArtist().Name)
	}
	asRelease := rel.AsRelease()
	if asRelease.ID != rel.ID || asRelease.ReleaseGroup.ID != rel.ReleaseGroup.ID {
		t.Errorf("AsRelease() = %+v, want identity fields to match", asRelease)
	}
}

func TestLookupReleaseWithTracklistNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})

	if _, err := c.LookupReleaseWithTracklist(t.Context(), "does-not-exist"); err == nil {
		t.Error("expected an error for a 404 response")
	}
}

func TestReleaseWithTracklistPrimaryArtistEmpty(t *testing.T) {
	var rel ReleaseWithTracklist
	if got := rel.PrimaryArtist(); got.ID != "" {
		t.Errorf("PrimaryArtist() = %+v, want zero value for a release with no artist-credit", got)
	}
}
