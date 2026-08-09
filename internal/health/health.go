// Package health runs LibriNode's background health checks: root folders
// still reachable, indexers answering, download clients up, metadata token
// valid. Results are cached; the UI shows them as a warning banner and the
// System page lists them with a re-check button.
package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/librinode/librinode/internal/download"
	"github.com/librinode/librinode/internal/indexer"
	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/metadata"
)

// Issue levels: errors mean something configured is broken; warnings flag a
// gap that limits what LibriNode can do.
const (
	LevelError   = "error"
	LevelWarning = "warning"
)

// Issue is one health finding.
type Issue struct {
	Source  string `json:"source"` // root-folder, indexer, download-client, metadata
	Level   string `json:"level"`
	Message string `json:"message"`
}

// Result is a completed check run.
type Result struct {
	Issues    []Issue   `json:"issues"`
	CheckedAt time.Time `json:"checkedAt"`
}

// checkTimeout bounds each external connection test.
const checkTimeout = 15 * time.Second

type Service struct {
	store     *library.Store
	indexers  *indexer.Service
	downloads *download.Service
	metadata  *metadata.Manager

	mu   sync.RWMutex
	last Result
}

func New(store *library.Store, indexers *indexer.Service, downloads *download.Service, providers *metadata.Manager) *Service {
	return &Service{store: store, indexers: indexers, downloads: downloads, metadata: providers}
}

// Last returns the most recent result; CheckedAt is zero when no check has
// run yet.
func (s *Service) Last() Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.last
}

// Check runs every health check now, caches and returns the result. Errors
// sort before warnings so the banner leads with what's broken.
func (s *Service) Check(ctx context.Context) Result {
	issues := []Issue{}
	issues = append(issues, s.checkRootFolders()...)
	issues = append(issues, s.checkMetadata(ctx)...)
	issues = append(issues, s.checkIndexers(ctx)...)
	issues = append(issues, s.checkDownloadClients(ctx)...)
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].Level == LevelError && issues[j].Level != LevelError
	})

	result := Result{Issues: issues, CheckedAt: time.Now().UTC()}
	s.mu.Lock()
	s.last = result
	s.mu.Unlock()
	if len(issues) > 0 {
		slog.Info("health check found issues", "count", len(issues))
	}
	return result
}

// RunPeriodic re-checks on an interval; the first check runs after a short
// delay so startup isn't slowed by connection tests.
func (s *Service) RunPeriodic(ctx context.Context, interval time.Duration) {
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return
	}
	s.Check(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.Check(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) checkRootFolders() []Issue {
	folders, err := s.store.ListRootFolders()
	if err != nil {
		return []Issue{{Source: "root-folder", Level: LevelError, Message: "Listing root folders failed: " + err.Error()}}
	}
	issues := []Issue{}
	for _, f := range folders {
		if info, err := os.Stat(f.Path); err != nil || !info.IsDir() {
			issues = append(issues, Issue{
				Source: "root-folder",
				Level:  LevelError,
				Message: fmt.Sprintf("Root folder %s (%s) is not accessible — files there can't be scanned or imported",
					f.Path, f.MediaType),
			})
		}
	}
	return issues
}

func (s *Service) checkMetadata(ctx context.Context) []Issue {
	issues := []Issue{}

	p := s.metadata.Current()
	if p == nil {
		issues = append(issues, Issue{
			Source:  "metadata",
			Level:   LevelWarning,
			Message: "No metadata provider configured — search, add, and refresh are disabled. Add a token under Settings → Metadata.",
		})
	} else {
		issues = append(issues, s.validateProvider(ctx, "metadata", p.Name(), p)...)
	}

	// Manga/comic series providers, checked only for libraries actually set
	// up — a user who never touches manga shouldn't see banners about
	// AniList. "None" (deliberately disabled) reports nil here, same as an
	// unconfigured book provider would, but that's a valid choice, not a
	// problem, so it's silent rather than a warning.
	statuses, err := s.store.LibraryStatuses()
	if err != nil {
		return issues
	}
	for _, st := range statuses {
		if !st.Active {
			continue
		}
		var sp metadata.SeriesProvider
		switch st.MediaType {
		case "manga":
			sp = s.metadata.SeriesFor("manga")
		case "comic":
			sp = s.metadata.SeriesFor("comic")
		default:
			continue
		}
		if sp == nil {
			continue
		}
		issues = append(issues, s.validateProvider(ctx, "metadata-"+st.MediaType, sp.Name(), sp)...)
	}
	return issues
}

// providerNamer is the sliver of metadata.Provider/SeriesProvider that
// validateProvider needs — satisfied by both.
type providerNamer interface {
	Name() string
}

// validateProvider runs a provider's cheap Validate() call, if it has one,
// and turns a failure into an Issue — worded as "unreachable" (warning,
// self-healing) when the provider never responded, or "rejected its
// token/key" (error, needs a fix) otherwise.
func (s *Service) validateProvider(ctx context.Context, source, name string, p providerNamer) []Issue {
	v, ok := p.(metadata.Validator)
	if !ok {
		return nil // no cheap validation call; don't burn quota on searches
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	err := v.Validate(cctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, metadata.ErrUnreachable) {
		return []Issue{{
			Source:  source,
			Level:   LevelWarning,
			Message: fmt.Sprintf("Metadata provider %s is unreachable: %v", name, err),
		}}
	}
	return []Issue{{
		Source:  source,
		Level:   LevelError,
		Message: fmt.Sprintf("Metadata provider %s rejected its token: %v", name, err),
	}}
}

func (s *Service) checkIndexers(ctx context.Context) []Issue {
	enabled, err := s.indexers.Store().ListEnabled()
	if err != nil {
		return []Issue{{Source: "indexer", Level: LevelError, Message: "Listing indexers failed: " + err.Error()}}
	}
	if len(enabled) == 0 {
		return []Issue{{
			Source:  "indexer",
			Level:   LevelWarning,
			Message: "No enabled indexers — nothing can be searched or grabbed. Add one under Settings → Indexers.",
		}}
	}
	issues := []Issue{}
	for i := range enabled {
		ind := &enabled[i]
		// An indexer already resting after repeated search failures (e.g.
		// stuck 429ing) is skipped here — probing it again every health cycle
		// would only add load to something searches are already avoiding.
		// Report the resting state itself instead, so the banner explains
		// *why* it's currently unusable and when it'll be tried again.
		if until, resting := s.indexers.Resting(ind.ID); resting {
			issues = append(issues, Issue{
				Source: "indexer",
				Level:  LevelWarning,
				Message: fmt.Sprintf("Indexer %q is resting after repeated failures — next retry at %s",
					ind.Name, until.Local().Format("15:04 MST")),
			})
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err := s.indexers.Test(cctx, ind)
		cancel()
		if err != nil {
			issues = append(issues, Issue{
				Source:  "indexer",
				Level:   LevelWarning,
				Message: fmt.Sprintf("Indexer %q failed its connection check: %v", ind.Name, err),
			})
		}
	}
	return issues
}

func (s *Service) checkDownloadClients(ctx context.Context) []Issue {
	configs, err := s.downloads.Store().List()
	if err != nil {
		return []Issue{{Source: "download-client", Level: LevelError, Message: "Listing download clients failed: " + err.Error()}}
	}
	enabled := []download.ClientConfig{}
	for _, c := range configs {
		if c.Enabled {
			enabled = append(enabled, c)
		}
	}
	if len(enabled) == 0 {
		return []Issue{{
			Source:  "download-client",
			Level:   LevelWarning,
			Message: "No enabled download clients — grabbed releases have nowhere to go. Add one under Settings → Download Clients.",
		}}
	}
	issues := []Issue{}
	for i := range enabled {
		cfg := &enabled[i]
		client, err := download.New(cfg)
		if err != nil {
			issues = append(issues, Issue{
				Source:  "download-client",
				Level:   LevelError,
				Message: fmt.Sprintf("Download client %q is misconfigured: %v", cfg.Name, err),
			})
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout)
		err = client.Test(cctx)
		cancel()
		if err != nil {
			issues = append(issues, Issue{
				Source:  "download-client",
				Level:   LevelError,
				Message: fmt.Sprintf("Download client %q is unreachable: %v", cfg.Name, err),
			})
		}
	}
	return issues
}
