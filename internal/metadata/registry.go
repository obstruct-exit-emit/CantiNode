package metadata

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Settings holds one provider's configuration. Every current provider needs
// at most a token; provider-specific fields (base URL, ...) get added here
// as new sources land, so the config file and settings API don't change
// shape per provider.
type Settings struct {
	Token string `yaml:"token" json:"token"`
	// Language, Country, and IncludeAdult are the GLOBAL metadata
	// preferences (Settings → Metadata), injected at build time
	// (config.ProviderSettings) so every provider — current and future —
	// can honor them without provider-specific settings. Providers prefer
	// editions/entries matching them and fall back to less strict picks;
	// a provider without the data ignores them. Metadata only — quality
	// profiles own acquisition. Never persisted per provider.
	Language     string `yaml:"-" json:"-"`
	Country      string `yaml:"-" json:"-"`
	IncludeAdult bool   `yaml:"-" json:"-"`
	// IncludeCompilations, when false (the default), hides box sets, omnibus
	// editions, and other collections from search so results are individual
	// books; a provider that flags compilations honors it, others ignore it.
	IncludeCompilations bool `yaml:"-" json:"-"`
}

// Factory builds a provider from its settings. Returning ErrNotConfigured
// means "valid but disabled" (e.g. no token yet) — not a hard error.
type Factory func(Settings) (Provider, error)

// Validator is optionally implemented by providers that can cheaply verify
// their credentials against the live API (used by the settings Test button).
type Validator interface {
	Validate(ctx context.Context) error
}

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register makes a provider available under name. Registering the same name
// again replaces the factory (convenient for tests).
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[name] = f
}

// Available lists registered provider names, sorted.
func Available() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Build constructs a registered provider from settings.
func Build(name string, s Settings) (Provider, error) {
	regMu.RLock()
	f, ok := factories[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown metadata provider %q", name)
	}
	return f(s)
}

// SeriesFactory builds a series provider (manga/comic) from its settings.
// ErrNotConfigured means "valid but disabled" (e.g. no API key yet).
type SeriesFactory func(Settings) (SeriesProvider, error)

var seriesFactories = map[string]SeriesFactory{}

// RegisterSeries makes a series provider available under name.
func RegisterSeries(name string, f SeriesFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	seriesFactories[name] = f
}

// seriesRegistryKeys lists the raw registration keys, sorted. A provider can
// be registered under several keys — one per media type it serves (Hardcover
// has "hardcover" for manga and "hardcover-comics" for comics) — while
// reporting one Name() everywhere user-visible.
func seriesRegistryKeys() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	keys := make([]string, 0, len(seriesFactories))
	for key := range seriesFactories {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// SeriesAvailable lists the distinct series-provider names as the providers
// report them — what settings entries and series.Source use — sorted.
func SeriesAvailable() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	seen := map[string]bool{}
	names := []string{}
	for _, f := range seriesFactories {
		// Probe builds only read Name(); token-requiring factories still
		// report it via a throwaway instance.
		if p, err := f(Settings{Token: "probe"}); err == nil && !seen[p.Name()] {
			seen[p.Name()] = true
			names = append(names, p.Name())
		}
	}
	sort.Strings(names)
	return names
}

// BuildSeries constructs a registered series provider from settings.
func BuildSeries(name string, s Settings) (SeriesProvider, error) {
	regMu.RLock()
	f, ok := seriesFactories[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown series provider %q", name)
	}
	return f(s)
}

// Manager holds the active provider and allows swapping it at runtime, so
// saving a token in the settings UI takes effect without a restart. The
// zero-ish state (no provider) makes metadata operations return
// ErrNotConfigured.
type Manager struct {
	mu           sync.RWMutex
	active       Provider
	series       map[string]SeriesProvider // selected provider per media type
	seriesByName map[string]SeriesProvider // every configured provider, by name
	// settings as last passed to Configure, so per-record provider overrides
	// can build a non-active book provider by name.
	bookSettings map[string]Settings
}

func NewManager() *Manager {
	return &Manager{}
}

// Current returns the active provider, or nil when none is configured.
func (m *Manager) Current() Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Set replaces the active provider directly (tests and special wiring).
func (m *Manager) Set(p Provider) {
	m.mu.Lock()
	m.active = p
	m.mu.Unlock()
}

// Configure builds and activates the named provider from settings, with no
// fallbacks. See ConfigureWithFallbacks.
func (m *Manager) Configure(name string, settings map[string]Settings) error {
	return m.ConfigureWithFallbacks(name, nil, settings)
}

// ConfigureWithFallbacks builds and activates the named provider, wrapped in
// the ordered fallbacks when any are usable (see NewFallback). An empty name,
// or a factory reporting ErrNotConfigured, deactivates metadata cleanly;
// other build errors on the primary leave the previous provider in place. A
// fallback that fails to build or reports ErrNotConfigured is skipped, never
// fatal — a bad fallback must not take metadata down. The settings map is
// retained (copied) for by-name builds (ProviderByName).
func (m *Manager) ConfigureWithFallbacks(name string, fallbacks []string, settings map[string]Settings) error {
	retained := make(map[string]Settings, len(settings))
	for k, v := range settings {
		retained[k] = v
	}
	m.mu.Lock()
	m.bookSettings = retained
	m.mu.Unlock()

	if name == "" {
		m.Set(nil)
		return nil
	}
	p, err := Build(name, settings[name])
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			m.Set(nil)
			return nil
		}
		return err
	}
	var chain []Provider
	for _, fb := range fallbacks {
		if fb == "" || fb == name {
			continue
		}
		fp, ferr := Build(fb, settings[fb])
		if ferr != nil {
			continue // a disabled or unbuildable fallback is simply skipped
		}
		chain = append(chain, fp)
	}
	m.Set(NewFallback(p, chain...))
	return nil
}

// ProviderByName returns the named book provider for a per-record override:
// the active provider when the name matches, otherwise one built from the
// retained settings. Nil when unknown or unconfigured.
func (m *Manager) ProviderByName(name string) Provider {
	if name == "" {
		return nil
	}
	if p := m.Current(); p != nil && p.Name() == name {
		return p
	}
	m.mu.RLock()
	s := m.bookSettings[name]
	m.mu.RUnlock()
	p, err := Build(name, s)
	if err != nil {
		return nil
	}
	return p
}

// ConfigureSeries (re)builds every registered series provider from settings;
// providers reporting ErrNotConfigured are left inactive. For each media type,
// selection[mediaType] names the preferred provider; when it's unset or that
// provider isn't configured, the first available one (by sorted name) wins.
func (m *Manager) ConfigureSeries(settings map[string]Settings, selection map[string]string) {
	byType := map[string][]SeriesProvider{}
	byName := map[string]SeriesProvider{}
	for _, key := range seriesRegistryKeys() { // sorted, so [0] is a stable default
		// A registry key's settings live under the provider's OWN name
		// (Hardcover's comic registration reuses the hardcover token): probe
		// for the name first, then build with that name's settings.
		probe, err := BuildSeries(key, Settings{Token: "probe"})
		if err != nil {
			continue
		}
		p, err := BuildSeries(key, settings[probe.Name()])
		if err != nil {
			continue
		}
		byType[p.MediaType()] = append(byType[p.MediaType()], p)
		// Keyed by the provider's own name (which series.Source records) — a
		// provider registered under several keys collapses to one entry; the
		// instances are interchangeable for by-source refreshes.
		byName[p.Name()] = p
	}

	built := map[string]SeriesProvider{}
	for mt, providers := range byType {
		// "none" disables the media type's provider outright: no search, no
		// adds; existing series still refresh through their own source
		// (SeriesProviderByName).
		if selection[mt] == "none" {
			continue
		}
		chosen := providers[0]
		for _, p := range providers {
			if p.Name() == selection[mt] {
				chosen = p
				break
			}
		}
		built[mt] = chosen
	}
	m.mu.Lock()
	m.series = built
	m.seriesByName = byName
	m.mu.Unlock()
}

// SeriesProviderByName returns a configured series provider by its stable
// name — used to refresh an existing series through the provider that
// created it, even when a different provider is now selected for that media
// type.
func (m *Manager) SeriesProviderByName(name string) SeriesProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.seriesByName[name]
}

// AvailableSeriesProviders lists the series-provider names (as the providers
// report them — what series.Source records and the selection matches) that
// serve a media type, configured or not, sorted — for the settings UI.
func AvailableSeriesProviders(mediaType string) []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := []string{}
	for _, f := range seriesFactories {
		// Build with a probe setting only to read Name/MediaType; providers
		// that need a token still report them via a throwaway instance.
		if p, err := f(Settings{Token: "probe"}); err == nil && p.MediaType() == mediaType {
			names = append(names, p.Name())
		}
	}
	sort.Strings(names)
	return names
}

// SeriesFor returns the active series provider for a media type, or nil.
func (m *Manager) SeriesFor(mediaType string) SeriesProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.series[mediaType]
}

// SetSeries injects a series provider directly (tests).
func (m *Manager) SetSeries(p SeriesProvider) {
	m.mu.Lock()
	if m.series == nil {
		m.series = map[string]SeriesProvider{}
	}
	if m.seriesByName == nil {
		m.seriesByName = map[string]SeriesProvider{}
	}
	m.series[p.MediaType()] = p
	m.seriesByName[p.Name()] = p
	m.mu.Unlock()
}
