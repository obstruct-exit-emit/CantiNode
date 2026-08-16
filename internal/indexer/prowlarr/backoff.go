package prowlarr

import (
	"fmt"
	"sync"
	"time"
)

// Per-sub-indexer failure backoff — the same shape as
// internal/indexer/search.go's own top-level indexer backoff (2m base,
// doubling per consecutive failure, capped at 20m, one success clears it,
// in-memory only), but scoped to this package: a Prowlarr connection is
// one CantiNode indexer row fanning out to many of Prowlarr's own
// sub-indexers, each of which needs its own rest state keyed by both IDs
// together, not the single int64 row ID search.go's tracker uses. Once a
// sub-indexer (e.g. a slow scraped torrent site) racks up enough
// consecutive timeouts, Search stops even attempting it for a while —
// CantiNode learns which one is bad and stops paying its cost, without
// needing Prowlarr to report anything itself.
const (
	subIndexerBackoffBase = 2 * time.Minute
	subIndexerBackoffMax  = 20 * time.Minute
	subIndexerRestAfter   = 3 // consecutive failures before a sub-indexer starts resting
)

type subIndexerBackoffState struct {
	failures int
	until    time.Time
}

// subIndexerBackoffTracker is a package-level singleton rather than a
// field on searcher: indexer.Service.searchOne constructs a fresh searcher
// via NativeDef.New on every single search call, so state living on the
// struct itself would reset before it could ever accumulate. A package
// singleton persists for the process lifetime, the same way
// search.go's own Service.backoff does for top-level indexers.
type subIndexerBackoffTracker struct {
	mu    sync.Mutex
	state map[string]*subIndexerBackoffState
	now   func() time.Time // test hook
}

var subIndexerBackoff = &subIndexerBackoffTracker{
	state: map[string]*subIndexerBackoffState{},
	now:   time.Now,
}

func subIndexerKey(cantinodeIndexerID int64, subIndexerID int) string {
	return fmt.Sprintf("%d:%d", cantinodeIndexerID, subIndexerID)
}

// resting reports whether cantinodeIndexerID's sub-indexer subIndexerID is
// currently in backoff.
func (t *subIndexerBackoffTracker) resting(cantinodeIndexerID int64, subIndexerID int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.state[subIndexerKey(cantinodeIndexerID, subIndexerID)]
	if !ok {
		return false
	}
	return t.now().Before(st.until)
}

// record updates backoff state after one search attempt against a
// sub-indexer — nil err clears any existing rest, a non-nil err (a real
// failure or a perSubIndexerTimeout expiring) counts toward the next rest.
func (t *subIndexerBackoffTracker) record(cantinodeIndexerID int64, subIndexerID int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := subIndexerKey(cantinodeIndexerID, subIndexerID)
	if err == nil {
		delete(t.state, key)
		return
	}
	st := t.state[key]
	if st == nil {
		st = &subIndexerBackoffState{}
		t.state[key] = st
	}
	st.failures++
	if st.failures < subIndexerRestAfter {
		return
	}
	rest := subIndexerBackoffBase << (st.failures - subIndexerRestAfter)
	if rest > subIndexerBackoffMax || rest <= 0 {
		rest = subIndexerBackoffMax
	}
	st.until = t.now().Add(rest)
}
