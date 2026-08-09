package indexer

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Failure backoff: an indexer that keeps erroring rests instead of being
// hammered every sweep. The first restAfter consecutive failures are tolerated
// (a scraped source like AudioBook Bay bounces intermittently on a shared/VPN
// IP but recovers on the next try — one blip shouldn't hide it for minutes);
// past that, rest doubles per failure (2m, 4m, 8m, …) capped at 20m. One
// success clears it. In-memory — a restart forgives.
//
// The rest is deliberately short: a big wanted search can briefly overrun an
// indexer's (or Prowlarr's) rate limit, and those indexers recover within
// seconds — resting a still-healthy indexer for hours (the user then has to
// restart the app to clear it) is worse than the momentary overload. Keeping
// the cap low means the worst case self-heals in minutes without a restart.
const (
	backoffBase = 2 * time.Minute
	backoffMax  = 20 * time.Minute
	restAfter   = 3 // consecutive failures before an indexer starts resting
)

type backoffState struct {
	failures int
	until    time.Time
}

// Service ties the store and client together for multi-indexer operations.
type Service struct {
	store  *Store
	client *Client

	mu      sync.Mutex
	backoff map[int64]*backoffState
	now     func() time.Time // test hook
}

func NewService(store *Store) *Service {
	return &Service{store: store, client: NewClient(), backoff: map[int64]*backoffState{}, now: time.Now}
}

func (s *Service) Store() *Store   { return s.store }
func (s *Service) Client() *Client { return s.client }

// searchOne runs one indexer's search, dispatching a native source to its
// registered implementation and everything else to the Newznab/Torznab client.
// A native source that doesn't serve the media type yields nothing (not an error).
func (s *Service) searchOne(ctx context.Context, ind *Indexer, query, nativeQuery, mediaType string) ([]Release, error) {
	if def, ok := NativeDefFor(ind.Type); ok {
		if !def.Serves(mediaType) {
			return nil, nil
		}
		return def.New(ind, s.client.httpc).Search(ctx, nativeQuery, mediaType)
	}
	return s.client.Search(ctx, ind, query, ind.CategoriesFor(mediaType))
}

// Test verifies an indexer definition, dispatching native sources to their
// implementation and API indexers to the Newznab/Torznab caps check.
func (s *Service) Test(ctx context.Context, ind *Indexer) error {
	if def, ok := NativeDefFor(ind.Type); ok {
		return def.New(ind, s.client.httpc).Test(ctx)
	}
	return s.client.Test(ctx, ind)
}

// ResolveGrabURL turns a release's download URL into the real downloadable one
// at grab time. Most URLs (magnets, direct links, Newznab guids) pass straight
// through; a native source that defers resolution (implements Resolver) and
// owns the URL's host gets to rewrite it — e.g. AudioBook Bay turning its
// release-page URL into an assembled magnet. Best-effort: any failure to look
// up an owner returns the URL unchanged so a grab is never blocked on this.
func (s *Service) ResolveGrabURL(ctx context.Context, downloadURL string) (string, error) {
	u, err := url.Parse(downloadURL)
	if err != nil || u.Host == "" {
		return downloadURL, nil
	}
	indexers, err := s.store.ListEnabled()
	if err != nil {
		return downloadURL, nil
	}
	for i := range indexers {
		ind := indexers[i]
		def, ok := NativeDefFor(ind.Type)
		if !ok || !nativeOwnsHost(&ind, def, u.Host) {
			continue
		}
		if r, ok := def.New(&ind, s.client.httpc).(Resolver); ok {
			return r.Resolve(ctx, downloadURL)
		}
	}
	return downloadURL, nil
}

// nativeOwnsHost reports whether a URL host belongs to one of a native
// indexer's configured site URLs (or its default).
func nativeOwnsHost(ind *Indexer, def NativeDef, host string) bool {
	raw := strings.TrimSpace(ind.BaseURL)
	if raw == "" {
		raw = def.DefaultBaseURL
	}
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if u, err := url.Parse(p); err == nil && strings.EqualFold(u.Host, host) {
			return true
		}
	}
	return false
}

// resting reports whether an indexer is in backoff, without mutating state.
func (s *Service) resting(id int64) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.backoff[id]
	if !ok || s.now().After(st.until) {
		return time.Time{}, false
	}
	return st.until, true
}

// Resting reports whether an indexer is currently in failure backoff (see
// resting) — exported so the health check can skip probing an indexer that
// searches are already avoiding, instead of adding load to something already
// known to be failing.
func (s *Service) Resting(id int64) (time.Time, bool) {
	return s.resting(id)
}

// recordResult updates the backoff state after a search attempt.
func (s *Service) recordResult(id int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.backoff, id)
		return
	}
	st := s.backoff[id]
	if st == nil {
		st = &backoffState{}
		s.backoff[id] = st
	}
	st.failures++
	if st.failures < restAfter {
		return // still within the tolerated streak — keep trying it next time
	}
	rest := backoffBase << (st.failures - restAfter)
	if rest > backoffMax || rest <= 0 {
		rest = backoffMax
	}
	st.until = s.now().Add(rest)
}

// SearchAll queries every enabled indexer concurrently — using each
// indexer's category list for the media type — and merges the results,
// sorted by seeders (torrents first by health) then size. Indexers that
// fail are reported in errs without sinking the whole search, and repeat
// offenders rest with exponential backoff instead of being retried.
//
// nativeQuery is the query handed to native scraped sources (AudioBook Bay,
// Library Genesis) instead of query: those match a title as a contiguous
// phrase, so an author-prefixed keyword (query) finds nothing — the caller
// passes the bare book/series title here. Empty falls back to query.
func (s *Service) SearchAll(ctx context.Context, query, nativeQuery, mediaType string) (releases []Release, errs []string, err error) {
	if nativeQuery == "" {
		nativeQuery = query
	}
	indexers, err := s.store.ListEnabled()
	if err != nil {
		return nil, nil, err
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	releases = []Release{}
	errs = []string{}

	for i := range indexers {
		ind := indexers[i]
		if until, ok := s.resting(ind.ID); ok {
			errs = append(errs, fmt.Sprintf("%s: resting after repeated failures (until %s)",
				ind.Name, until.UTC().Format("15:04 MST")))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			found, err := s.searchOne(ctx, &ind, query, nativeQuery, mediaType)
			s.recordResult(ind.ID, err)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", ind.Name, err))
				return
			}
			releases = append(releases, found...)
		}()
	}
	wg.Wait()

	sort.SliceStable(releases, func(a, b int) bool {
		if releases[a].Seeders != releases[b].Seeders {
			return releases[a].Seeders > releases[b].Seeders
		}
		return releases[a].Size > releases[b].Size
	})
	sort.Strings(errs)
	return releases, errs, nil
}
