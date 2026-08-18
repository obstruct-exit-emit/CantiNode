package discography

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/musicbrainz"
	"github.com/cantinode/cantinode/internal/musiclibrary"
)

func newTestService(t *testing.T, handler http.HandlerFunc) (*Service, *musiclibrary.Store) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := musiclibrary.NewStore(db)
	mb := musicbrainz.NewClientWithBaseURL("0.1.0-test", "", srv.URL)
	return New(mb, store), store
}

func TestRefreshArtistCachesDiscographyAndMetadata(t *testing.T) {
	s, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/release-group/" {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"release-group-count": 1,
			"release-groups": []map[string]any{
				{"id": "rg-1", "title": "Geogaddi", "primary-type": "Album", "first-release-date": "2002-02-04"},
			},
		})
	})

	artist, err := store.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
	if err != nil {
		t.Fatal(err)
	}

	mbArtist := &musicbrainz.Artist{
		ID:     "artist-mbid",
		Name:   "Boards of Canada",
		Genres: []musicbrainz.Genre{{Name: "idm"}},
		Tags:   []musicbrainz.Tag{{Name: "scottish"}},
		Rating: musicbrainz.Rating{Value: 4.5, VotesCount: 42},
	}
	groups, err := s.RefreshArtist(context.Background(), artist.ID, mbArtist)
	if err != nil {
		t.Fatalf("RefreshArtist: %v", err)
	}
	if len(groups) != 1 || groups[0].ReleaseGroupMBID != "rg-1" || groups[0].Title != "Geogaddi" {
		t.Fatalf("groups = %+v", groups)
	}

	cached, err := store.ListArtistReleaseGroups(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].ReleaseGroupMBID != "rg-1" {
		t.Fatalf("cached release groups = %+v, want [rg-1]", cached)
	}

	refreshed, err := store.GetArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Genres) != 1 || refreshed.Genres[0] != "idm" {
		t.Errorf("Genres = %v, want [idm]", refreshed.Genres)
	}
	if refreshed.RatingValue != 4.5 || refreshed.RatingVotes != 42 {
		t.Errorf("Rating = %v/%v, want 4.5/42", refreshed.RatingValue, refreshed.RatingVotes)
	}
	if refreshed.LastSyncedAt == nil {
		t.Error("LastSyncedAt should be set after a refresh")
	}
}

// TestRefreshArtistSkipsVariousArtistsDiscography proves the special-cased
// skip for musicbrainz.VariousArtistsMBID: never calls MusicBrainz to
// browse its (effectively unbounded) release-group list, and clears any
// stale rows an earlier, pre-fix run may have already cached for it —
// confirmed live: the real "Various Artists" artist had accumulated 10,000
// bogus "missing" rows this way before this fix existed.
func TestRefreshArtistSkipsVariousArtistsDiscography(t *testing.T) {
	s, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("RefreshArtist should never call MusicBrainz for Various Artists, got request to %q", r.URL.Path)
	})

	artist, err := store.GetOrCreateArtist(musicbrainz.VariousArtistsMBID, "Various Artists", "Various Artists")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate stale rows left over from before this fix existed.
	if err := store.ReplaceArtistReleaseGroups(artist.ID, []musiclibrary.ReleaseGroupCache{
		{ReleaseGroupMBID: "rg-stale", Title: "Some Compilation", PrimaryType: "Album"},
	}); err != nil {
		t.Fatal(err)
	}

	mbArtist := &musicbrainz.Artist{ID: musicbrainz.VariousArtistsMBID, Name: "Various Artists"}
	groups, err := s.RefreshArtist(context.Background(), artist.ID, mbArtist)
	if err != nil {
		t.Fatalf("RefreshArtist: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}

	cached, err := store.ListArtistReleaseGroups(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 0 {
		t.Errorf("cached release groups = %+v, want none — stale rows should be cleared", cached)
	}

	refreshed, err := store.GetArtist(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.LastSyncedAt == nil {
		t.Error("LastSyncedAt should still be set, so this isn't retried as if it never ran")
	}
}

func TestRefreshSeriesCachesDiscography(t *testing.T) {
	s, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("RefreshSeries should never call MusicBrainz itself -- it's given an already-looked-up series")
	})

	artist, err := store.GetOrCreateSeriesArtist("series-mbid", "Now That's What I Call Music!")
	if err != nil {
		t.Fatal(err)
	}

	series := &musicbrainz.Series{
		ID:   "series-mbid",
		Name: "Now That's What I Call Music!",
		Relations: []musicbrainz.SeriesReleaseGroupRelation{
			{OrderingKey: 1, ReleaseGroupMBID: "rg-now-1", Title: "NOW 1", PrimaryType: "Album"},
			{OrderingKey: 2, ReleaseGroupMBID: "rg-now-2", Title: "NOW 2", PrimaryType: "Album"},
		},
	}
	groups, err := s.RefreshSeries(context.Background(), artist.ID, series)
	if err != nil {
		t.Fatalf("RefreshSeries: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}

	cached, err := store.ListArtistReleaseGroups(artist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 2 {
		t.Fatalf("cached release groups = %+v, want 2", cached)
	}
}

// TestRefreshDispatchesByKind proves the kind-branching entry point --
// what internal/discoveryrefresh's periodic sweep actually calls -- looks
// the artist up via the right MusicBrainz endpoint for its own kind.
func TestRefreshDispatchesByKind(t *testing.T) {
	t.Run("artist", func(t *testing.T) {
		var hitArtist, hitReleaseGroups bool
		s, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/artist/artist-mbid":
				hitArtist = true
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "artist-mbid", "name": "Boards of Canada"})
			case "/release-group/":
				hitReleaseGroups = true
				_ = json.NewEncoder(w).Encode(map[string]any{"release-group-count": 0, "release-groups": []any{}})
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		})
		artist, err := store.GetOrCreateArtist("artist-mbid", "Boards of Canada", "Boards of Canada")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Refresh(context.Background(), artist); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if !hitArtist || !hitReleaseGroups {
			t.Errorf("hitArtist=%v hitReleaseGroups=%v, want both true", hitArtist, hitReleaseGroups)
		}
	})

	t.Run("series", func(t *testing.T) {
		var hitSeries bool
		s, store := newTestService(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path != "/series/series-mbid" {
				t.Errorf("unexpected path %q, want the series lookup", r.URL.Path)
				return
			}
			hitSeries = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "series-mbid", "name": "NOW",
				"relations": []map[string]any{
					{
						"target-type":  "release_group",
						"ordering-key": 1,
						"release_group": map[string]any{
							"id": "rg-now-1", "title": "NOW 1", "primary-type": "Album",
						},
					},
				},
			})
		})
		artist, err := store.GetOrCreateSeriesArtist("series-mbid", "Now That's What I Call Music!")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Refresh(context.Background(), artist); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if !hitSeries {
			t.Error("Refresh should have looked the series up via LookupSeries")
		}
	})
}
