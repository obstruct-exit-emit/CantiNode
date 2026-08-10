package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cantinode/cantinode/internal/database"
	"github.com/cantinode/cantinode/internal/download"
	"github.com/cantinode/cantinode/internal/indexer"
	"github.com/cantinode/cantinode/internal/library"
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
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, ok); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO root_folders (media_type, path) VALUES ('music', ?)`, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	svc := New(
		library.NewStore(db),
		indexer.NewService(indexer.NewStore(db)),
		download.NewService(download.NewStore(db)),
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
	// indexers and no download clients.
	levels := map[string]string{}
	counts := map[string]int{}
	for _, is := range res.Issues {
		levels[is.Source] = is.Level
		counts[is.Source]++
	}
	want := map[string]string{
		"root-folder":     LevelError,
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
		AudioCategories: "3010,3040", Enabled: true,
	}
	if err := idxSvc.Store().Add(ind); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Fail the search enough times to enter backoff — the first few consecutive
	// failures are tolerated (a flaky source shouldn't rest on one blip).
	for i := 0; i < 5; i++ {
		if _, _, err := idxSvc.SearchAll(ctx, "test query", "", "music"); err != nil {
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
		download.NewService(download.NewStore(db)),
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
