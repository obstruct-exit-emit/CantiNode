// Package config loads and persists LibriNode's server configuration.
//
// Precedence (highest wins): environment variables (LIBRINODE_*),
// values in <dataDir>/config.yaml, built-in defaults. The config file is
// created with defaults (including a freshly generated API key) on first run.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/librinode/librinode/internal/metadata"
)

// MetadataSettings selects the active metadata provider and stores each
// provider's settings (kept even while inactive, so switching back is
// painless).
type MetadataSettings struct {
	Active    string                       `yaml:"active"`
	Providers map[string]metadata.Settings `yaml:"providers"`
	// Fallbacks names book providers, in order, consulted only when Active
	// draws a blank on a search or an id lookup — the "as fallbacks" contract.
	// A record found through a fallback is stored under that fallback's name,
	// so its later refresh routes back to it, not to Active. Open Library and
	// Google Books are the keyless fallbacks that ship. See
	// metadata.FallbackProvider.
	Fallbacks []string `yaml:"fallbacks,omitempty"`
	// MangaProvider chooses the manga series provider ("anilist",
	// "hardcover", or "none" to disable); empty defaults to anilist.
	// ComicProvider chooses the comic series provider ("hardcover",
	// "comicvine", or "none"); empty defaults to hardcover. "none" turns off
	// search/adds for that library — existing series still refresh through
	// their own source.
	MangaProvider string `yaml:"manga_provider,omitempty"`
	ComicProvider string `yaml:"comic_provider,omitempty"`
	// MangaCoverSource / ComicCoverSource pick volume/issue cover art per
	// library: "file" (extract the first page of the owned archive) or
	// "provider" (the metadata provider's art). Both default to provider art.
	MangaCoverSource string `yaml:"manga_cover_source,omitempty"`
	ComicCoverSource string `yaml:"comic_cover_source,omitempty"`
	// Language / Country / IncludeAdult are global, provider-agnostic
	// metadata preferences: every provider that carries the data prefers
	// matching editions/entries and falls back to less strict picks.
	// Defaults: english, united states, adult content hidden; "none" means
	// no preference at all. They shape METADATA only — acquisition (quality
	// profiles) is untouched.
	Language     string `yaml:"language,omitempty"`
	Country      string `yaml:"country,omitempty"`
	IncludeAdult bool   `yaml:"include_adult,omitempty"`
	// IncludeCompilations, off by default, keeps box sets / omnibus editions /
	// collections out of metadata search so results are individual books.
	IncludeCompilations bool `yaml:"include_compilations,omitempty"`
}

// MangaSeriesProvider returns the configured manga provider name, defaulting
// to anilist.
func (c *Config) MangaSeriesProvider() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata.MangaProvider == "" {
		return "anilist"
	}
	return c.Metadata.MangaProvider
}

// ComicSeriesProvider returns the configured comic provider name, defaulting
// to hardcover.
func (c *Config) ComicSeriesProvider() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata.ComicProvider == "" {
		return "hardcover"
	}
	return c.Metadata.ComicProvider
}

// MetadataLanguage returns the global metadata language preference,
// defaulting to english.
func (c *Config) MetadataLanguage() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata.Language == "" {
		return "english"
	}
	return c.Metadata.Language
}

// MetadataCountry returns the global metadata country preference, defaulting
// to united states.
func (c *Config) MetadataCountry() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Metadata.Country == "" {
		return "united states"
	}
	return c.Metadata.Country
}

// IncludeAdult reports whether adult-flagged results may appear in metadata
// searches (default: hidden).
func (c *Config) IncludeAdult() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Metadata.IncludeAdult
}

// IncludeCompilations reports whether box sets / omnibus editions may appear in
// metadata searches (default: hidden).
func (c *Config) IncludeCompilations() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Metadata.IncludeCompilations
}

// ProviderSettings returns the providers map with the global metadata
// preferences injected into every entry — providers are built from Settings
// alone, so the preferences ride along to each of them, present and future.
// A "none" language/country means no preference and is injected as empty.
func (c *Config) ProviderSettings() map[string]metadata.Settings {
	ms := c.MetadataSettings()
	lang, country, adult := c.MetadataLanguage(), c.MetadataCountry(), c.IncludeAdult()
	comps := c.IncludeCompilations()
	if lang == "none" {
		lang = ""
	}
	if country == "none" {
		country = ""
	}
	for name, s := range ms.Providers {
		s.Language, s.Country, s.IncludeAdult = lang, country, adult
		s.IncludeCompilations = comps
		ms.Providers[name] = s
	}
	return ms.Providers
}

// CoverSourceFor returns the effective volume-cover source ("file" or
// "provider") for a manga/comic media type: the per-type setting, or the
// default — the provider's art.
func (c *Config) CoverSourceFor(mediaType string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var v string
	switch mediaType {
	case "manga":
		v = c.Metadata.MangaCoverSource
	case "comic":
		v = c.Metadata.ComicCoverSource
	}
	if v == "" {
		return "provider"
	}
	return v
}

// UseProviderCovers reports whether a media type's volume covers should come
// from the metadata provider instead of the owned file.
func (c *Config) UseProviderCovers(mediaType string) bool {
	return c.CoverSourceFor(mediaType) == "provider"
}

// SeriesSelection maps each series media type to its chosen provider, for
// metadata.Manager.ConfigureSeries.
func (c *Config) SeriesSelection() map[string]string {
	return map[string]string{
		"manga": c.MangaSeriesProvider(),
		"comic": c.ComicSeriesProvider(),
	}
}

// NamingSettings holds the file-organization templates (per media type as
// later phases land; ebooks first). Rendered per path segment by the naming
// package.
type NamingSettings struct {
	EbookFolder string `yaml:"ebook_folder" json:"ebookFolder"`
	EbookFile   string `yaml:"ebook_file" json:"ebookFile"`
	// Audiobooks use Audiobookshelf's Author/Book-folder layout: the "file"
	// template names the per-book folder (and the audio file inside, for
	// single-file books).
	AudiobookFolder string `yaml:"audiobook_folder" json:"audiobookFolder"`
	AudiobookFile   string `yaml:"audiobook_file" json:"audiobookFile"`
	// Manga/comics use Kavita/Komga's Series/File layout.
	MangaFolder string `yaml:"manga_folder" json:"mangaFolder"`
	MangaFile   string `yaml:"manga_file" json:"mangaFile"`
	ComicFolder string `yaml:"comic_folder" json:"comicFolder"`
	ComicFile   string `yaml:"comic_file" json:"comicFile"`
	// Magazines: issue books are titled "Magazine - <date/issue>", so the
	// file template can lean on {Book Title}.
	MagazineFolder string `yaml:"magazine_folder" json:"magazineFolder"`
	MagazineFile   string `yaml:"magazine_file" json:"magazineFile"`
}

func defaultNaming() NamingSettings {
	return NamingSettings{
		// Each ebook lives in its own folder (Calibre/Readarr convention) so
		// its sidecars travel with it; the filename stays informative on its
		// own — author, series, title, year.
		EbookFolder:     "{Author Name}/{Book Title} ({Release Year})",
		EbookFile:       "{Author Name} - {Series Title} {Series Position} - {Book Title} ({Release Year})",
		AudiobookFolder: "{Author Name}",
		AudiobookFile:   "{Series Title} {Series Position} - {Book Title} ({Release Year})",
		MangaFolder:     "{Series Title}",
		MangaFile:       "{Series Title} Vol. {Series Position 00} ({Release Year})",
		ComicFolder:     "{Series Title}",
		ComicFile:       "{Series Title} #{Series Position 00} ({Release Year})",
		// Magazines accumulate; year subfolders keep the pile browsable.
		MagazineFolder: "{Series Title}/{Release Year}",
		MagazineFile:   "{Book Title}",
	}
}

// UserAccount is one login. Passwords are stored only as PBKDF2 hashes.
// Exactly one user is the default: the protected primary account — it can't
// be removed, only superseded by promoting another user to default.
// Roles: admin has full access (settings, indexers, download clients,
// backups, logs, user management, API key); member gets everything else —
// browsing, monitoring, search, scan, grab, organize — but not the server's
// own configuration or other accounts. A self-hosted household's common
// shape: one or two admins (the owner, maybe a partner) and everyone else
// as members.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type UserAccount struct {
	Username     string `yaml:"username" json:"username"`
	PasswordHash string `yaml:"password_hash" json:"-"`
	Default      bool   `yaml:"default,omitempty" json:"default"`
	// Role is RoleAdmin or RoleMember; empty means admin (see EffectiveRole)
	// — every account saved before roles existed keeps full access on
	// upgrade rather than being silently downgraded.
	Role string `yaml:"role,omitempty" json:"role"`
}

// EffectiveRole returns the account's role, defaulting to admin for
// accounts from before roles existed (Load backfills this on disk too, so
// it's only ever relevant for the moment between reading the file and the
// first save completing).
func (u UserAccount) EffectiveRole() string {
	if u.Role == "" {
		return RoleAdmin
	}
	return u.Role
}

// AuthSettings holds the optional login accounts. No users means
// authentication is disabled (the UI falls back to the API-key prompt).
type AuthSettings struct {
	// Legacy single account from pre-multi-user config files; migrated into
	// Users on load and dropped from the file on the next save.
	Username     string        `yaml:"username,omitempty"`
	PasswordHash string        `yaml:"password_hash,omitempty"`
	Users        []UserAccount `yaml:"users,omitempty"`
}

// Enabled reports whether any login account is configured.
func (a AuthSettings) Enabled() bool { return len(a.Users) > 0 }

// Find returns the account with the given username (exact match), or nil.
func (a AuthSettings) Find(username string) *UserAccount {
	for i := range a.Users {
		if a.Users[i].Username == username {
			return &a.Users[i]
		}
	}
	return nil
}

// ImportSettings tunes Completed Download Handling. All three default to on
// (see defaults()); the fields carry no omitempty so an explicit "off" is
// written to the file and survives a reload instead of falling back to the
// default.
type ImportSettings struct {
	// PackImportAll imports every matching book from a multi-book pack, not
	// just monitored ones. On by default. Off fills monitored books only, so
	// grabbing one volume never auto-imports the rest of a bundle.
	PackImportAll bool `yaml:"pack_import_all" json:"packImportAll"`
	// RemoveCompleted removes a download from its client once LibriNode has
	// imported it. On by default. Off leaves torrents seeding until their
	// client's own goal is met (usenet history is cleared either way).
	RemoveCompleted bool `yaml:"remove_completed" json:"removeCompleted"`
	// DeleteCompletedFiles also deletes the downloaded files from disk when a
	// download is removed after import. On by default; implies RemoveCompleted.
	DeleteCompletedFiles bool `yaml:"delete_completed_files" json:"deleteCompletedFiles"`
}

// PathMapping translates a download client's reported path prefix into the
// path where LibriNode actually sees those files — for setups where the
// client runs on another machine or in a container and reports its own
// filesystem ("/storage_1/…") while the same share is mounted here somewhere
// else ("/mnt/media/…"). The longest matching prefix wins.
type PathMapping struct {
	RemotePrefix string `yaml:"remote" json:"remotePrefix"`
	LocalPrefix  string `yaml:"local" json:"localPrefix"`
}

// TranslatePath applies the longest matching path mapping to a
// client-reported path; unmatched paths pass through unchanged. Matches are
// boundary-aware ("/data" maps "/data/x" but never "/database/x"), and the
// remainder's separators are converted to the local prefix's style so a
// Windows client path maps cleanly onto a Unix mount (and vice versa).
func TranslatePath(mappings []PathMapping, p string) string {
	if p == "" {
		return p
	}
	best := -1
	bestLen := 0
	for i, m := range mappings {
		remote := strings.TrimRight(m.RemotePrefix, `/\`)
		if remote == "" || len(p) < len(remote) || !strings.EqualFold(p[:len(remote)], remote) {
			continue
		}
		if len(p) > len(remote) && p[len(remote)] != '/' && p[len(remote)] != '\\' {
			continue // "/database" must not match a "/data" mapping
		}
		if len(remote) > bestLen {
			best, bestLen = i, len(remote)
		}
	}
	if best < 0 {
		return p
	}
	local := strings.TrimRight(mappings[best].LocalPrefix, `/\`)
	rest := p[bestLen:]
	if strings.Contains(local, "/") || !strings.Contains(local, `\`) {
		rest = strings.ReplaceAll(rest, `\`, "/")
	} else {
		rest = strings.ReplaceAll(rest, "/", `\`)
	}
	return local + rest
}

// TimingSettings tunes the background loops. Zero values mean "use the
// default", so existing configs stay on defaults and the file only records
// deliberate choices. Changes apply on the next server start.
type TimingSettings struct {
	// SearchIntervalHours: automatic wanted-list sweep cadence (default 6).
	SearchIntervalHours int `yaml:"search_interval_hours,omitempty" json:"searchIntervalHours"`
	// RefreshIntervalHours: library metadata re-sync cadence (default 720 —
	// 30 days; metadata rarely changes, and a monthly sweep is kinder to
	// providers. New volumes for monitored series still arrive via per-item
	// refreshes and manual Refresh.)
	RefreshIntervalHours int `yaml:"refresh_interval_hours,omitempty" json:"refreshIntervalHours"`
	// HealthIntervalMinutes: background health check cadence (default 15).
	HealthIntervalMinutes int `yaml:"health_interval_minutes,omitempty" json:"healthIntervalMinutes"`
	// ImportIntervalSeconds: Completed Download Handling poll cadence
	// (default 60).
	ImportIntervalSeconds int `yaml:"import_interval_seconds,omitempty" json:"importIntervalSeconds"`
}

// Resolved intervals, defaults applied.

func (t TimingSettings) SearchInterval() time.Duration {
	if t.SearchIntervalHours > 0 {
		return time.Duration(t.SearchIntervalHours) * time.Hour
	}
	return 6 * time.Hour
}

func (t TimingSettings) RefreshInterval() time.Duration {
	if t.RefreshIntervalHours > 0 {
		return time.Duration(t.RefreshIntervalHours) * time.Hour
	}
	return 720 * time.Hour // 30 days
}

func (t TimingSettings) HealthInterval() time.Duration {
	if t.HealthIntervalMinutes > 0 {
		return time.Duration(t.HealthIntervalMinutes) * time.Minute
	}
	return 15 * time.Minute
}

func (t TimingSettings) ImportInterval() time.Duration {
	if t.ImportIntervalSeconds > 0 {
		return time.Duration(t.ImportIntervalSeconds) * time.Second
	}
	return time.Minute
}

type Config struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	APIKey   string `yaml:"api_key"`
	LogLevel string `yaml:"log_level"` // debug, info, warn, error

	Auth     AuthSettings     `yaml:"auth,omitempty"`
	Metadata MetadataSettings `yaml:"metadata"`
	Naming   NamingSettings   `yaml:"naming"`
	// No omitempty: Import defaults to all-on, so an all-off choice must be
	// written explicitly rather than dropped and re-defaulted on load.
	Import  ImportSettings `yaml:"import"`
	Timings TimingSettings `yaml:"timings,omitempty"`
	// PathMappingList translates client-reported download paths onto this
	// machine's filesystem (Completed Download Handling reads them).
	PathMappingList []PathMapping `yaml:"path_mappings,omitempty"`

	// Legacy flat field, migrated into Metadata.Providers on load and
	// dropped from the file on the next save.
	LegacyHardcoverToken string `yaml:"hardcover_token,omitempty"`

	mu      sync.Mutex
	dataDir string
}

func defaults() *Config {
	return &Config{
		Host:     "0.0.0.0",
		Port:     7845,
		LogLevel: "info",
		Metadata: MetadataSettings{
			Active:    "hardcover",
			Providers: map[string]metadata.Settings{},
		},
		Naming: defaultNaming(),
		// Completed Download Handling is fully automatic by default: import
		// whole packs, remove the download from its client, and delete the
		// source files once imported.
		Import: ImportSettings{
			PackImportAll:        true,
			RemoveCompleted:      true,
			DeleteCompletedFiles: true,
		},
	}
}

// DefaultDataDir returns the OS-appropriate data directory:
// %AppData%\LibriNode on Windows, ~/.config/librinode on Linux.
func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "librinode"
	if runtime.GOOS == "windows" {
		name = "LibriNode"
	}
	return filepath.Join(base, name), nil
}

// Load reads the config from dataDir (or the OS default when empty),
// creating the directory and a default config file on first run.
func Load(dataDir string) (*Config, error) {
	if dataDir == "" {
		var err error
		if dataDir, err = DefaultDataDir(); err != nil {
			return nil, fmt.Errorf("resolving default data dir: %w", err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}

	cfg := defaults()
	cfg.dataDir = dataDir

	path := cfg.filePath()
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// First run: fall through and persist defaults below.
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	default:
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
	}

	applyEnvOverrides(cfg)

	if cfg.Metadata.Providers == nil {
		cfg.Metadata.Providers = map[string]metadata.Settings{}
	}
	// Migrate the legacy flat token into the provider map; omitempty drops
	// the old field from the file on save.
	if cfg.LegacyHardcoverToken != "" {
		if cfg.Metadata.Providers["hardcover"].Token == "" {
			cfg.setProviderToken("hardcover", cfg.LegacyHardcoverToken)
		}
		cfg.LegacyHardcoverToken = ""
	}
	// Migrate the legacy single login account into the user list (as the
	// default); omitempty drops the old fields from the file on save.
	if cfg.Auth.Username != "" {
		if cfg.Auth.Find(cfg.Auth.Username) == nil {
			cfg.Auth.Users = append(cfg.Auth.Users, UserAccount{
				Username:     cfg.Auth.Username,
				PasswordHash: cfg.Auth.PasswordHash,
				Default:      true,
			})
		}
		cfg.Auth.Username, cfg.Auth.PasswordHash = "", ""
	}
	// Every account already on disk before roles existed gets admin — the
	// safe, non-downgrading default. New accounts always specify a role via
	// AddUser, so this only ever touches pre-existing records, and only
	// until the next save fills the field in on disk.
	for i := range cfg.Auth.Users {
		if cfg.Auth.Users[i].Role == "" {
			cfg.Auth.Users[i].Role = RoleAdmin
		}
	}
	normalizeUsers(&cfg.Auth)
	if v := os.Getenv("LIBRINODE_HARDCOVER_TOKEN"); v != "" {
		cfg.setProviderToken("hardcover", v)
	}

	// Empty templates (fresh section, hand-edited file) fall back to defaults.
	cfg.Naming.FillDefaults()

	if cfg.APIKey == "" {
		cfg.APIKey = newAPIKey()
	}

	// Persist so the generated API key (and any new defaults) survive restarts.
	if err := cfg.save(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("LIBRINODE_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("LIBRINODE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}
	if v := os.Getenv("LIBRINODE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("LIBRINODE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
}

func (c *Config) setProviderToken(provider, token string) {
	s := c.Metadata.Providers[provider]
	s.Token = token
	c.Metadata.Providers[provider] = s
}

// SetMetadata replaces the metadata settings and persists the config.
// Safe for concurrent use from API handlers.
func (c *Config) SetMetadata(ms MetadataSettings) error {
	c.mu.Lock()
	c.Metadata = ms
	c.mu.Unlock()
	return c.save()
}

// FillDefaults replaces empty templates with the built-in defaults, so a
// partial update (or hand-edited config) can never leave a media type with
// an empty — and thus garbage-rendering — template.
func (ns *NamingSettings) FillDefaults() {
	def := defaultNaming()
	fill := func(dst *string, fallback string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = fallback
		}
	}
	fill(&ns.EbookFolder, def.EbookFolder)
	fill(&ns.EbookFile, def.EbookFile)
	fill(&ns.AudiobookFolder, def.AudiobookFolder)
	fill(&ns.AudiobookFile, def.AudiobookFile)
	fill(&ns.MangaFolder, def.MangaFolder)
	fill(&ns.MangaFile, def.MangaFile)
	fill(&ns.ComicFolder, def.ComicFolder)
	fill(&ns.ComicFile, def.ComicFile)
	fill(&ns.MagazineFolder, def.MagazineFolder)
	fill(&ns.MagazineFile, def.MagazineFile)
}

// SetNaming replaces the naming templates and persists the config. Empty
// fields fall back to defaults rather than being stored.
func (c *Config) SetNaming(ns NamingSettings) error {
	ns.FillDefaults()
	c.mu.Lock()
	c.Naming = ns
	c.mu.Unlock()
	return c.save()
}

// NamingSettings returns the current naming templates.
func (c *Config) NamingSettings() NamingSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Naming
}

// ImportSettings returns the current import options.
func (c *Config) ImportSettings() ImportSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Import
}

// SetImport replaces the import options and persists the config.
func (c *Config) SetImport(is ImportSettings) error {
	c.mu.Lock()
	c.Import = is
	c.mu.Unlock()
	return c.save()
}

// PackImportAll reports whether pack imports fill every matching book
// instead of monitored ones only.
func (c *Config) PackImportAll() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Import.PackImportAll
}

// TimingSettings returns the background-loop cadences (zero = default).
func (c *Config) TimingSettings() TimingSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Timings
}

// PathMappings returns a copy of the remote→local path mappings.
func (c *Config) PathMappings() []PathMapping {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]PathMapping, len(c.PathMappingList))
	copy(out, c.PathMappingList)
	return out
}

// SetPathMappings validates, replaces, and persists the path mappings.
func (c *Config) SetPathMappings(mappings []PathMapping) error {
	clean := make([]PathMapping, 0, len(mappings))
	for _, m := range mappings {
		m.RemotePrefix = strings.TrimSpace(m.RemotePrefix)
		m.LocalPrefix = strings.TrimSpace(m.LocalPrefix)
		if m.RemotePrefix == "" || m.LocalPrefix == "" {
			return fmt.Errorf("path mapping needs both a remote and a local prefix")
		}
		clean = append(clean, m)
	}
	c.mu.Lock()
	c.PathMappingList = clean
	c.mu.Unlock()
	return c.save()
}

// SetTimings validates, replaces, and persists the background-loop cadences.
// Zero fields mean "default"; set fields are clamped to sane ranges so a typo
// can't hammer indexers or stall the importer.
func (c *Config) SetTimings(t TimingSettings) error {
	clamp := func(v, min, max int) int {
		if v <= 0 {
			return 0 // default
		}
		if v < min {
			return min
		}
		if v > max {
			return max
		}
		return v
	}
	t.SearchIntervalHours = clamp(t.SearchIntervalHours, 1, 168)
	t.RefreshIntervalHours = clamp(t.RefreshIntervalHours, 6, 2160)
	t.HealthIntervalMinutes = clamp(t.HealthIntervalMinutes, 5, 1440)
	t.ImportIntervalSeconds = clamp(t.ImportIntervalSeconds, 30, 3600)
	c.mu.Lock()
	c.Timings = t
	c.mu.Unlock()
	return c.save()
}

// MetadataSettings returns a deep copy so callers can't mutate shared state.
func (c *Config) MetadataSettings() MetadataSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := MetadataSettings{
		Active:              c.Metadata.Active,
		MangaProvider:       c.Metadata.MangaProvider,
		ComicProvider:       c.Metadata.ComicProvider,
		MangaCoverSource:    c.Metadata.MangaCoverSource,
		ComicCoverSource:    c.Metadata.ComicCoverSource,
		Language:            c.Metadata.Language,
		Country:             c.Metadata.Country,
		IncludeAdult:        c.Metadata.IncludeAdult,
		IncludeCompilations: c.Metadata.IncludeCompilations,
		Fallbacks:           append([]string(nil), c.Metadata.Fallbacks...),
		Providers:           make(map[string]metadata.Settings, len(c.Metadata.Providers)),
	}
	for name, s := range c.Metadata.Providers {
		out.Providers[name] = s
	}
	return out
}

// AuthSettings returns the current login accounts (possibly none). The Users
// slice is a copy — callers can't mutate shared state.
func (c *Config) AuthSettings() AuthSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.Auth
	out.Users = append([]UserAccount(nil), c.Auth.Users...)
	return out
}

// SetAuth replaces the login accounts and persists the config. An empty
// settings value disables authentication entirely.
func (c *Config) SetAuth(a AuthSettings) error {
	c.mu.Lock()
	c.Auth = a
	normalizeUsers(&c.Auth)
	c.mu.Unlock()
	return c.save()
}

// normalizeUsers keeps the account list coherent: exactly one default (the
// first user when none or several are flagged).
func normalizeUsers(a *AuthSettings) {
	seen := false
	for i := range a.Users {
		if a.Users[i].Default {
			if seen {
				a.Users[i].Default = false
			}
			seen = true
		}
	}
	if !seen && len(a.Users) > 0 {
		a.Users[0].Default = true
	}
}

// AddUser appends a login account with the given role (RoleAdmin or
// RoleMember; anything else — including "" — becomes RoleMember, the safer
// default for a newly added account). The first account becomes the
// protected default and is always admin regardless of what's requested,
// since the default being demotable could leave an instance with no admin
// at all.
func (c *Config) AddUser(username, passwordHash, role string) error {
	c.mu.Lock()
	for i := range c.Auth.Users {
		if strings.EqualFold(c.Auth.Users[i].Username, username) {
			c.mu.Unlock()
			return fmt.Errorf("user %q already exists", username)
		}
	}
	isFirst := len(c.Auth.Users) == 0
	if role != RoleAdmin {
		role = RoleMember
	}
	if isFirst {
		role = RoleAdmin
	}
	c.Auth.Users = append(c.Auth.Users, UserAccount{
		Username:     username,
		PasswordHash: passwordHash,
		Default:      isFirst,
		Role:         role,
	})
	c.mu.Unlock()
	return c.save()
}

// RemoveUser deletes a login account. The default user is protected — promote
// another user first.
func (c *Config) RemoveUser(username string) error {
	c.mu.Lock()
	for i := range c.Auth.Users {
		if c.Auth.Users[i].Username != username {
			continue
		}
		if c.Auth.Users[i].Default {
			c.mu.Unlock()
			return fmt.Errorf("the default user cannot be removed")
		}
		c.Auth.Users = append(c.Auth.Users[:i], c.Auth.Users[i+1:]...)
		c.mu.Unlock()
		return c.save()
	}
	c.mu.Unlock()
	return fmt.Errorf("user %q not found", username)
}

// SetUserPassword replaces one account's password hash.
func (c *Config) SetUserPassword(username, passwordHash string) error {
	c.mu.Lock()
	u := c.Auth.Find(username)
	if u == nil {
		c.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	u.PasswordHash = passwordHash
	c.mu.Unlock()
	return c.save()
}

// SetDefaultUser makes the named account the protected default, promoting
// it to admin in the same step if it wasn't already — the default-is-always-
// admin invariant (SetUserRole relies on it to refuse demoting the default)
// must hold no matter which direction an account became the default.
func (c *Config) SetDefaultUser(username string) error {
	c.mu.Lock()
	if c.Auth.Find(username) == nil {
		c.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	for i := range c.Auth.Users {
		c.Auth.Users[i].Default = c.Auth.Users[i].Username == username
		if c.Auth.Users[i].Username == username {
			c.Auth.Users[i].Role = RoleAdmin
		}
	}
	c.mu.Unlock()
	return c.save()
}

// SetUserRole changes an account's role. The default user can't be demoted
// — it's the one account guaranteed to survive removal, so keeping it an
// admin guarantees the instance always has at least one.
func (c *Config) SetUserRole(username, role string) error {
	if role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("role must be %q or %q", RoleAdmin, RoleMember)
	}
	c.mu.Lock()
	u := c.Auth.Find(username)
	if u == nil {
		c.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	if u.Default && role != RoleAdmin {
		c.mu.Unlock()
		return fmt.Errorf("the default user must stay an admin — promote another user to default first")
	}
	u.Role = role
	c.mu.Unlock()
	return c.save()
}

// CurrentAPIKey returns the API key, safe against concurrent regeneration.
func (c *Config) CurrentAPIKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.APIKey
}

// RegenerateAPIKey replaces the API key with a fresh one and persists it.
// Existing integrations (Prowlarr, scripts) must be updated to the new key.
func (c *Config) RegenerateAPIKey() (string, error) {
	c.mu.Lock()
	c.APIKey = newAPIKey()
	key := c.APIKey
	c.mu.Unlock()
	if err := c.save(); err != nil {
		return "", err
	}
	return key, nil
}

func newAPIKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

func (c *Config) save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.filePath(), out, 0o600)
}

func (c *Config) filePath() string     { return filepath.Join(c.dataDir, "config.yaml") }
func (c *Config) DataDir() string      { return c.dataDir }
func (c *Config) DatabasePath() string { return filepath.Join(c.dataDir, "librinode.db") }
func (c *Config) LogPath() string      { return filepath.Join(c.dataDir, "logs", "librinode.log") }

func (c *Config) ListenAddr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func (c *Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
