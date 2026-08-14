// Package download talks to download clients — qBittorrent for torrents,
// SABnzbd for usenet — behind one interface: send a release, watch its
// progress, remove it. A completed download is picked up like any other
// file by the next library scan (internal/musicscanner).
package download

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// sharedTransport is used by every download client's *http.Client (see
// sabnzbd.go/qbittorrent.go) instead of the zero-value default. Go's
// http.DefaultTransport closes an idle connection after 90s; internal/
// importer's background sweep (RunPeriodic) only touches each client every
// PollInterval (2 minutes) — longer than that default — so the pooled
// connection to a client was routinely cold by the next sweep. That's
// invisible for a real download client on the LAN, but a debrid-bridged
// one (qBittorrent/SABnzbd-compatible endpoints proxying to a cloud
// service) pays several real seconds to re-establish a connection, which
// showed up as the Activity page taking noticeably long to load the first
// time in a while. A much longer idle timeout keeps that connection warm
// across background sweeps, so only a genuinely fresh CantiNode process
// pays the cold-connect cost.
var sharedTransport = &http.Transport{
	Proxy:               http.ProxyFromEnvironment,
	MaxIdleConns:        20,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     10 * time.Minute,
}

const (
	TypeQBittorrent = "qbittorrent"
	TypeSABnzbd     = "sabnzbd"
	// TypeDirect is CantiNode's own HTTP fetcher — no external program; see
	// direct.go.
	TypeDirect = "direct"

	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
	// ProtocolDirect marks releases that are plain HTTP file links (possibly a
	// "|"-separated mirror list) downloaded by the direct client.
	ProtocolDirect = "direct"
)

// ErrNotFound is returned when a requested client config does not exist.
var ErrNotFound = errors.New("download client not found")

// ErrNoClient is returned when no enabled client handles a protocol.
var ErrNoClient = errors.New("no enabled download client for this protocol")

// ClientConfig is one configured download client.
type ClientConfig struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"apiKey"`
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	AddedAt  string `json:"addedAt"`
}

// Protocol reports which release protocol this client downloads.
func (c *ClientConfig) Protocol() string {
	switch c.Type {
	case TypeQBittorrent:
		return ProtocolTorrent
	case TypeDirect:
		return ProtocolDirect
	}
	return ProtocolUsenet
}

// Item is one download in a client, normalized across implementations.
// Status is one of: queued, downloading, paused, completed, seeded, failed
// (seeded = finished torrent the client has stopped seeding — goal reached).
type Item struct {
	Client   string  `json:"client"`
	ConfigID int64   `json:"clientConfigId"`
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"` // 0-1
	Path     string  `json:"path,omitempty"`
}

// Client is the operations CantiNode needs from any download client.
type Client interface {
	// Test verifies connectivity and credentials.
	Test(ctx context.Context) error
	// Add sends a release URL for download; the returned id may be empty
	// when the client doesn't report one (qBittorrent).
	Add(ctx context.Context, url, title string) (string, error)
	// List returns CantiNode's downloads (the client's category).
	List(ctx context.Context) ([]Item, error)
	// Remove deletes a download, optionally with its data.
	Remove(ctx context.Context, id string, deleteData bool) error
}

// New builds a protocol client from a config row.
func New(cfg *ClientConfig) (Client, error) {
	switch cfg.Type {
	case TypeQBittorrent:
		return newQBittorrent(cfg), nil
	case TypeSABnzbd:
		return newSABnzbd(cfg), nil
	case TypeDirect:
		return newDirect(cfg), nil
	}
	return nil, fmt.Errorf("unknown download client type %q", cfg.Type)
}

// --- Config store ---

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const cols = `id, name, type, host, username, password, api_key, category, enabled, priority, added_at`

func scanConfig(row interface{ Scan(...any) error }) (*ClientConfig, error) {
	var c ClientConfig
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Host, &c.Username, &c.Password,
		&c.APIKey, &c.Category, &c.Enabled, &c.Priority, &c.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) Add(c *ClientConfig) error {
	return s.db.QueryRow(`
		INSERT INTO download_clients (name, type, host, username, password, api_key, category, enabled, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING id, added_at`,
		c.Name, c.Type, c.Host, c.Username, c.Password, c.APIKey, c.Category, c.Enabled, c.Priority,
	).Scan(&c.ID, &c.AddedAt)
}

func (s *Store) Update(c *ClientConfig) error {
	res, err := s.db.Exec(`
		UPDATE download_clients
		SET name = ?, type = ?, host = ?, username = ?, password = ?, api_key = ?, category = ?, enabled = ?, priority = ?
		WHERE id = ?`,
		c.Name, c.Type, c.Host, c.Username, c.Password, c.APIKey, c.Category, c.Enabled, c.Priority, c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Get(id int64) (*ClientConfig, error) {
	return scanConfig(s.db.QueryRow(`SELECT `+cols+` FROM download_clients WHERE id = ?`, id))
}

func (s *Store) List() ([]ClientConfig, error) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM download_clients ORDER BY priority, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := []ClientConfig{}
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, *c)
	}
	return configs, rows.Err()
}

func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM download_clients WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Service ---

// queueCacheTTL is how long an aggregated queue snapshot is reused. Listing
// hits every download client live (a debrid bridge = several HTTP calls), so
// UI pollers — the Activity page plus any open book pages — share one snapshot
// instead of each stampeding the clients.
const queueCacheTTL = 15 * time.Second

// Service picks clients and aggregates across them.
type Service struct {
	store *Store

	mu           sync.Mutex
	cachedAt     time.Time
	cached       []Item
	cachedErr    []string
	cachedFailed map[int64]bool // client config id -> failed to answer, this snapshot
	clients      map[int64]clientEntry
	// sweepMu serializes cold queue sweeps: concurrent callers wait for the
	// one in flight and then read its snapshot instead of re-hitting clients.
	sweepMu sync.Mutex
	// urlResolver, if set, rewrites a release's download URL just before it's
	// handed to a client — for native sources that resolve lazily at grab time
	// (main wires this to the indexer service). Opaque here to avoid a
	// dependency on the indexer package.
	urlResolver func(ctx context.Context, downloadURL string) (string, error)
}

// SetURLResolver registers a grab-time download-URL rewriter (see urlResolver).
func (s *Service) SetURLResolver(fn func(ctx context.Context, downloadURL string) (string, error)) {
	s.urlResolver = fn
}

// clientEntry is a connected Client kept for reuse — a fresh qBittorrent
// client re-authenticates on every call (new cookie jar), doubling requests
// to the download client. The key detects config edits and forces a rebuild.
type clientEntry struct {
	key    string
	client Client
}

// clientKey fingerprints the connection-relevant config fields.
func clientKey(c *ClientConfig) string {
	return strings.Join([]string{c.Type, c.Host, c.Username, c.Password, c.APIKey, c.Category}, "\x00")
}

// client returns a cached Client for the config, building one on first use or
// after the config changed.
func (s *Service) client(cfg *ClientConfig) (Client, error) {
	key := clientKey(cfg)
	s.mu.Lock()
	if e, ok := s.clients[cfg.ID]; ok && e.key == key {
		s.mu.Unlock()
		return e.client, nil
	}
	s.mu.Unlock()

	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.clients == nil {
		s.clients = map[int64]clientEntry{}
	}
	s.clients[cfg.ID] = clientEntry{key: key, client: c}
	s.mu.Unlock()
	return c, nil
}

func NewService(store *Store) *Service {
	return &Service{store: store}
}

func (s *Service) Store() *Store { return s.store }

// InvalidateQueue marks the cached snapshot stale after anything that
// changes what the clients hold or which clients exist (a grab, a removal, a
// client config change) — the next Queue call still gets the old snapshot
// immediately (stale-while-revalidate, see Queue's own doc comment) rather
// than blocking on a fresh sweep, but that call also kicks one off in the
// background, so the change shows up within a poll cycle or two rather than
// needing queueCacheTTL to elapse naturally.
func (s *Service) InvalidateQueue() {
	s.mu.Lock()
	s.cachedAt = time.Time{}
	s.mu.Unlock()
}

// GrabResult reports where a release was sent.
type GrabResult struct {
	Client   string `json:"client"`
	ClientID int64  `json:"clientId"`
	ID       string `json:"id,omitempty"`
}

// Grab sends a release to the best enabled client for its protocol
// (lowest priority number wins).
func (s *Service) Grab(ctx context.Context, protocol, url, title string) (*GrabResult, error) {
	// Resolve lazily-deferred URLs (e.g. a scraped native source's release
	// page → assembled magnet) at the moment of grab, for exactly this
	// release.
	if s.urlResolver != nil {
		resolved, err := s.urlResolver(ctx, url)
		if err != nil {
			return nil, err
		}
		url = resolved
	}
	configs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	for i := range configs {
		cfg := &configs[i]
		if !cfg.Enabled || cfg.Protocol() != protocol {
			continue
		}
		client, err := s.client(cfg)
		if err != nil {
			return nil, err
		}
		id, err := client.Add(ctx, url, title)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Name, err)
		}
		s.InvalidateQueue()
		return &GrabResult{Client: cfg.Name, ClientID: cfg.ID, ID: id}, nil
	}
	return nil, ErrNoClient
}

// GrabRelease sends a release to the best client for its protocol and
// records the grab so the queue and grab history can show its progress.
// wantedAlbumID > 0 ties it to a wanted album, so internal/importer can
// update that row's status once the grab resolves; upgradeAlbumID > 0 ties
// it to an already-owned album instead (see handleGrabAlbumUpgrade), so
// internal/importer can swap the old file out once the better one is
// matched back in. At most one of the two is ever set — a grab is either
// for something not yet owned or an upgrade of something that already is,
// never both.
func (s *Service) GrabRelease(ctx context.Context, protocol, url, title, guid string, wantedAlbumID, upgradeAlbumID int64, mediaType string) (*GrabResult, *GrabRecord, error) {
	result, err := s.Grab(ctx, protocol, url, title)
	if err != nil {
		return nil, nil, err
	}
	grab := &GrabRecord{
		WantedAlbumID:  wantedAlbumID,
		UpgradeAlbumID: upgradeAlbumID,
		ClientConfigID: result.ClientID,
		ClientItemID:   result.ID,
		Title:          title,
		GUID:           guid,
		Protocol:       protocol,
		MediaType:      mediaType,
	}
	if err := s.store.AddGrab(grab); err != nil {
		return result, nil, fmt.Errorf("recording grab: %w", err)
	}
	return result, grab, nil
}

// Remove deletes an item from the client identified by its config id.
func (s *Service) Remove(ctx context.Context, configID int64, itemID string, deleteData bool) error {
	cfg, err := s.store.Get(configID)
	if err != nil {
		return err
	}
	client, err := s.client(cfg)
	if err != nil {
		return err
	}
	err = client.Remove(ctx, itemID, deleteData)
	s.InvalidateQueue()
	return err
}

// Queue aggregates the download queues of all enabled clients, serving a
// short-lived cached snapshot (queueCacheTTL) so concurrent UI pollers don't
// stampede the clients. Client failures come back as messages, not errors, so
// one dead client doesn't blank the whole view.
//
// Stale-while-revalidate once a snapshot exists at all: some clients (a
// debrid bridge in particular) can take several real seconds to answer even
// on a warm, reused connection — not a connection-warmup cost but genuine
// per-request latency on the bridge's own side — so blocking every cache
// expiry on a live sweep made the Activity page visibly hang each time it
// reloaded. Only the very first call for a freshly started process (nothing
// cached yet to fall back on) actually waits on a live sweep; every later
// stale hit gets the last snapshot immediately while a background sweep
// refreshes it for next time.
func (s *Service) Queue(ctx context.Context) ([]Item, []string, error) {
	s.mu.Lock()
	fresh := !s.cachedAt.IsZero() && time.Since(s.cachedAt) < queueCacheTTL
	hasSnapshot := s.cached != nil
	items, errs := s.cached, s.cachedErr
	s.mu.Unlock()

	if fresh {
		return items, errs, nil
	}
	if hasSnapshot {
		s.refreshQueueInBackground()
		return items, errs, nil
	}
	return s.sweepQueue(ctx)
}

// refreshQueueInBackground kicks off a live client sweep if one isn't
// already running, without blocking the caller. sweepMu (held for the
// sweep's whole duration) doubles as the in-flight guard: TryLock fails
// immediately if a sweep is already underway — whether that's an earlier
// background refresh or a foreground sweepQueue call — so a burst of stale
// pollers triggers at most one background refresh, not one per caller.
func (s *Service) refreshQueueInBackground() {
	if !s.sweepMu.TryLock() {
		return
	}
	go func() {
		defer s.sweepMu.Unlock()
		// Outlives the HTTP request that triggered it, so the sweep isn't
		// canceled the moment that request's own response is written — its
		// own generous, fixed timeout instead of the caller's context.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		s.sweepQueueLocked(ctx)
	}()
}

// sweepQueue is the blocking path: used only when there's no prior snapshot
// to fall back on. Serializes concurrent callers on sweepMu — the first one
// through actually sweeps; the rest wait here and then read the snapshot it
// just produced instead of re-hitting clients themselves.
func (s *Service) sweepQueue(ctx context.Context) ([]Item, []string, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	s.mu.Lock()
	if !s.cachedAt.IsZero() && time.Since(s.cachedAt) < queueCacheTTL {
		items, errs := s.cached, s.cachedErr
		s.mu.Unlock()
		return items, errs, nil
	}
	s.mu.Unlock()
	return s.sweepQueueLocked(ctx)
}

// sweepQueueLocked does the actual live sweep and updates the cache.
// Callers must already hold sweepMu.
func (s *Service) sweepQueueLocked(ctx context.Context) ([]Item, []string, error) {
	configs, err := s.store.List()
	if err != nil {
		return nil, nil, err
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		items  = []Item{}
		errs   = []string{}
		failed = map[int64]bool{}
	)
	for i := range configs {
		cfg := configs[i]
		if !cfg.Enabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := s.client(&cfg)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", cfg.Name, err))
				failed[cfg.ID] = true
				mu.Unlock()
				return
			}
			found, err := client.List(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", cfg.Name, err))
				failed[cfg.ID] = true
				return
			}
			items = append(items, found...)
		}()
	}
	wg.Wait()

	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Status != items[b].Status {
			return items[a].Status < items[b].Status
		}
		return strings.ToLower(items[a].Title) < strings.ToLower(items[b].Title)
	})
	sort.Strings(errs)

	s.mu.Lock()
	s.cached, s.cachedErr, s.cachedFailed, s.cachedAt = items, errs, failed, time.Now()
	s.mu.Unlock()
	return items, errs, nil
}

// FailedClients reports which download clients failed to answer during the
// last Queue() sweep (build error or List() error), keyed by config id. A
// pending grab whose client is in this set is not a true orphan — its client
// just didn't answer this pass — so the importer's orphan sweep exempts it
// instead of treating a client outage as every one of its grabs vanishing.
func (s *Service) FailedClients() map[int64]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]bool, len(s.cachedFailed))
	for id := range s.cachedFailed {
		out[id] = true
	}
	return out
}
