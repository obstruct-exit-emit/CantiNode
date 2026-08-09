package refresh

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

// fakeSeriesProvider is an in-memory metadata.SeriesProvider for manga tests.
type fakeSeriesProvider struct {
	name   string
	search []metadata.SeriesResult
	series map[string]*metadata.SeriesResult
}

func (f *fakeSeriesProvider) Name() string      { return f.name }
func (f *fakeSeriesProvider) MediaType() string { return "manga" }

func (f *fakeSeriesProvider) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return f.search, nil
}

func (f *fakeSeriesProvider) GetSeries(_ context.Context, id string) (*metadata.SeriesResult, error) {
	s, ok := f.series[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	return s, nil
}

// Switching the selected manga provider and refreshing must re-bind an existing
// series to the newly selected provider (by title match), in place — same local
// id, no duplicate series — and pull the new provider's per-volume descriptions.
func TestRefreshSeriesReMatchesToSelectedProvider(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	ani := &fakeSeriesProvider{
		name: "anifake",
		series: map[string]*metadata.SeriesResult{
			"a1": {ForeignID: "a1", Title: "Death Note", Description: "A notebook.", AuthorName: "Ohba",
				IssueCount: 2, Issues: []metadata.Issue{
					{ForeignID: "a1-v1", Number: 1},
					{ForeignID: "a1-v2", Number: 2},
				}},
		},
	}
	hc := &fakeSeriesProvider{
		name:   "hcfake",
		search: []metadata.SeriesResult{{ForeignID: "h1", Title: "Death Note", IssueCount: 2}},
		series: map[string]*metadata.SeriesResult{
			"h1": {ForeignID: "h1", Title: "Death Note", Description: "A notebook.", AuthorName: "Ohba",
				IssueCount: 2, Issues: []metadata.Issue{
					{ForeignID: "h1-v1", Number: 1, Description: "Boredom"},
					{ForeignID: "h1-v2", Number: 2, Description: "Confluence"},
				}},
		},
	}

	mgr := metadata.NewManager()
	mgr.SetSeries(ani) // manga provider = anifake
	svc := New(store, mgr)
	ctx := context.Background()

	added, err := svc.SyncSeries(ctx, "manga", "a1", true, true, true)
	if err != nil {
		t.Fatalf("SyncSeries: %v", err)
	}
	if added.Source != "anifake" {
		t.Fatalf("added source = %q, want anifake", added.Source)
	}

	mgr.SetSeries(hc) // user switches manga provider to hcfake

	if err := svc.RefreshSeries(ctx, added.ID); err != nil {
		t.Fatalf("RefreshSeries: %v", err)
	}

	got, err := store.GetSeries(added.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Source != "hcfake" || got.ForeignID != "h1" {
		t.Fatalf("series re-bind = %s/%s, want hcfake/h1", got.Source, got.ForeignID)
	}
	if got.ID != added.ID {
		t.Fatalf("series id changed %d -> %d (should re-bind in place)", added.ID, got.ID)
	}

	all, err := store.ListSeries("manga")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("manga series count = %d, want 1 (no duplicate)", len(all))
	}

	vols, err := store.ListVolumes(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volume count = %d, want 2 (old provider volumes dropped)", len(vols))
	}
	if vols[0].Description != "Boredom" || vols[1].Description != "Confluence" {
		t.Fatalf("volume descriptions = %q / %q, want Boredom / Confluence",
			vols[0].Description, vols[1].Description)
	}
	if vols[0].Source != "hcfake" {
		t.Fatalf("volume source = %q, want hcfake", vols[0].Source)
	}
}

// A same-provider refresh whose preferred edition changed (e.g. the global
// language preference flipped) must not duplicate volumes: the stale edition
// is retired and its monitored flag carries to the same-numbered replacement.
func TestRefreshSeriesRetiresStaleEditions(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := library.NewStore(db)

	hc := &fakeSeriesProvider{
		name: "hcfake",
		series: map[string]*metadata.SeriesResult{
			"h1": {ForeignID: "h1", Title: "Death Note", AuthorName: "Ohba",
				IssueCount: 2, Issues: []metadata.Issue{
					{ForeignID: "en-v1", Number: 1, Description: "English v1"},
					{ForeignID: "en-v2", Number: 2, Description: "English v2"},
				}},
		},
	}
	mgr := metadata.NewManager()
	mgr.SetSeries(hc)
	svc := New(store, mgr)
	ctx := context.Background()

	added, err := svc.SyncSeries(ctx, "manga", "h1", true, true, false)
	if err != nil {
		t.Fatalf("SyncSeries: %v", err)
	}
	// Monitor volume 2 (metadata-only adds start unmonitored).
	vols, _ := store.ListVolumes(added.ID)
	if err := store.SetBookMonitored(vols[1].ID, true); err != nil {
		t.Fatal(err)
	}

	// The provider now prefers different editions (language flipped).
	hc.series["h1"].Issues = []metadata.Issue{
		{ForeignID: "es-v1", Number: 1, Description: "Spanish v1"},
		{ForeignID: "es-v2", Number: 2, Description: "Spanish v2"},
	}
	if err := svc.RefreshSeries(ctx, added.ID); err != nil {
		t.Fatalf("RefreshSeries: %v", err)
	}

	vols, err = store.ListVolumes(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 2 {
		t.Fatalf("volume count = %d, want 2 (stale editions retired, not duplicated): %+v", len(vols), vols)
	}
	if vols[0].ForeignID != "es-v1" || vols[1].ForeignID != "es-v2" {
		t.Fatalf("volumes = %q/%q, want the new editions", vols[0].ForeignID, vols[1].ForeignID)
	}
	if vols[0].Monitored {
		t.Fatalf("volume 1 was unmonitored — replacement must stay unmonitored")
	}
	if !vols[1].Monitored {
		t.Fatalf("volume 2 was monitored — the monitoring must carry to the replacement")
	}
}
