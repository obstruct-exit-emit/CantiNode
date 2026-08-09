package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/librinode/librinode/internal/database"
	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

func TestCheckFindsIssues(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// One root folder that exists, one that has vanished since it was added.
	ok := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ mt, path string }{{"ebook", ok}, {"audiobook", gone}} {
		if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES (?, ?)`, f.mt, f.path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	svc := New(
		library.NewStore(db),
		indexer.NewService(indexer.NewStore(db)),
		download.NewService(download.NewStore(db)),
		metadata.NewManager(),
	)

	if !svc.Last().CheckedAt.IsZero() {
		t.Error("Last() before any check should have zero CheckedAt")
	}

	res := svc.Check(context.Background())
	if res.CheckedAt.IsZero() {
		t.Error("Check() result missing CheckedAt")
	}
	if got := svc.Last(); got.CheckedAt != res.CheckedAt {
		t.Error("Last() should return the cached result of Check()")
	}

	// Expected: the vanished root folder (error), plus warnings for no
	// metadata provider, no indexers, and no download clients.
	levels := map[string]string{}
	counts := map[string]int{}
	for _, is := range res.Issues {
		levels[is.Source] = is.Level
		counts[is.Source]++
	}
	want := map[string]string{
		"root-folder":     LevelError,
		"metadata":        LevelWarning,
		"indexer":         LevelWarning,
		"download-client": LevelWarning,
	}
	for src, lvl := range want {
		if levels[src] != lvl {
			t.Errorf("source %s: level = %q, want %q (issues: %+v)", src, levels[src], lvl, res.Issues)
		}
	}
	if counts["root-folder"] != 1 {
		t.Errorf("root-folder issues = %d, want 1 (the accessible folder must not be flagged)", counts["root-folder"])
	}
	if len(res.Issues) > 0 && res.Issues[0].Level != LevelError {
		t.Errorf("issues not sorted errors-first: %+v", res.Issues)
	}
}

// TestCheckIndexerRestingSkipsProbe: an indexer already in backoff after
// repeated search failures (the "stuck 429ing" case) must not be hit again
// by the health check — it should report the resting state, not add another
// probe on top of something already known to be failing.
func TestCheckIndexerRestingSkipsProbe(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	probes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	idxSvc := indexer.NewService(indexer.NewStore(db))
	ind := &indexer.Indexer{
		Name: "ratelimited", Type: indexer.TypeTorznab, BaseURL: srv.URL,
		Categories: "7000", Enabled: true,
	}
	if err := idxSvc.Store().Add(ind); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Fail the search enough times to enter backoff — the first few consecutive
	// failures are tolerated (a flaky source shouldn't rest on one blip).
	for i := 0; i < 5; i++ {
		if _, _, err := idxSvc.SearchAll(ctx, "test query", "", "ebook"); err != nil {
			t.Fatal(err)
		}
		if _, resting := idxSvc.Resting(ind.ID); resting {
			break
		}
	}
	if _, resting := idxSvc.Resting(ind.ID); !resting {
		t.Fatal("expected the indexer to be resting after repeated failed searches")
	}
	probesAfterSearch := probes

	svc := New(
		library.NewStore(db), idxSvc,
		download.NewService(download.NewStore(db)), metadata.NewManager(),
	)
	res := svc.Check(ctx)

	if probes != probesAfterSearch {
		t.Errorf("health check probed a resting indexer (probes %d -> %d) instead of skipping it",
			probesAfterSearch, probes)
	}
	found := false
	for _, is := range res.Issues {
		if is.Source == "indexer" && strings.Contains(is.Message, "resting") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'resting' indexer issue, got %+v", res.Issues)
	}
}

// unreachableProvider always fails Validate with metadata.ErrUnreachable,
// simulating a provider that's down rather than one rejecting a bad token.
type unreachableProvider struct{ name string }

func (p unreachableProvider) Name() string { return p.name }
func (unreachableProvider) SearchAuthors(context.Context, string) ([]metadata.Author, error) {
	return nil, nil
}
func (unreachableProvider) SearchBooks(context.Context, string) ([]metadata.Book, error) {
	return nil, nil
}
func (unreachableProvider) GetAuthor(context.Context, string) (*metadata.Author, error) {
	return nil, nil
}
func (unreachableProvider) GetBook(context.Context, string) (*metadata.Book, error) { return nil, nil }
func (unreachableProvider) Validate(context.Context) error {
	return fmt.Errorf("connection refused: %w", metadata.ErrUnreachable)
}

// unreachableSeriesProvider is unreachableProvider's manga/comic counterpart.
type unreachableSeriesProvider struct {
	name      string
	mediaType string
}

func (p unreachableSeriesProvider) Name() string      { return p.name }
func (p unreachableSeriesProvider) MediaType() string { return p.mediaType }
func (unreachableSeriesProvider) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (unreachableSeriesProvider) GetSeries(context.Context, string) (*metadata.SeriesResult, error) {
	return nil, nil
}
func (unreachableSeriesProvider) Validate(context.Context) error {
	return fmt.Errorf("connection refused: %w", metadata.ErrUnreachable)
}

// TestCheckSeriesMetadataScopedToActiveLibraries: a manga provider outage
// only produces a banner when the manga library is actually set up (a root
// folder exists) — a user who never touches manga shouldn't see AniList
// noise, but someone who does gets told when it's unreachable.
func TestCheckSeriesMetadataScopedToActiveLibraries(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mgr := metadata.NewManager()
	mgr.SetSeries(unreachableSeriesProvider{name: "fake-manga", mediaType: "manga"})

	svc := New(
		library.NewStore(db),
		indexer.NewService(indexer.NewStore(db)),
		download.NewService(download.NewStore(db)),
		mgr,
	)

	hasSeriesIssue := func(res Result) bool {
		for _, is := range res.Issues {
			if is.Source == "metadata-manga" {
				return true
			}
		}
		return false
	}

	if res := svc.Check(context.Background()); hasSeriesIssue(res) {
		t.Error("no manga root folder set up yet — should not report the manga provider at all")
	}

	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('manga', ?)`, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	res := svc.Check(context.Background())
	if !hasSeriesIssue(res) {
		t.Fatalf("manga library is active — expected a metadata-manga issue, got %+v", res.Issues)
	}
	for _, is := range res.Issues {
		if is.Source == "metadata-manga" && is.Level != LevelWarning {
			t.Errorf("unreachable series provider level = %s, want warning", is.Level)
		}
	}
}

// TestCheckMetadataUnreachableIsWarningNotError: a provider that never
// responds (down, DNS failure, timeout) is worded and leveled differently
// from one that responds and rejects the token — the former is transient and
// self-healing, not something the user needs to go fix.
func TestCheckMetadataUnreachableIsWarningNotError(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mgr := metadata.NewManager()
	mgr.Set(unreachableProvider{name: "fake"})

	svc := New(
		library.NewStore(db),
		indexer.NewService(indexer.NewStore(db)),
		download.NewService(download.NewStore(db)),
		mgr,
	)
	res := svc.Check(context.Background())

	var found *Issue
	for i := range res.Issues {
		if res.Issues[i].Source == "metadata" {
			found = &res.Issues[i]
		}
	}
	if found == nil {
		t.Fatal("expected a metadata issue")
	}
	if found.Level != LevelWarning {
		t.Errorf("unreachable provider level = %s, want warning", found.Level)
	}
	if !strings.Contains(found.Message, "unreachable") {
		t.Errorf("message = %q, want it to say unreachable", found.Message)
	}
}
