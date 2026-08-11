// Package config loads and persists CantiNode's server configuration.
//
// Precedence (highest wins): environment variables (CANTINODE_*),
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
)

// NamingSettings holds the file-organization templates. Music has its own
// template shape ({Artist}/{Album}/{Title}) and is rendered by
// internal/musicscanner's own FormatPath, not internal/naming — MusicFile is
// stored here anyway so it lives alongside other Settings sections.
type NamingSettings struct {
	// MusicFile is a single template (not folder+file) since
	// internal/musicscanner.FormatPath renders one path — folder
	// separators included — from artist/album/track in one placeholder
	// pass, same as {Artist}/{Album}/{TrackNumber} - {Title}.{Ext}.
	MusicFile string `yaml:"music_file" json:"musicFile"`
}

func defaultNaming() NamingSettings {
	return NamingSettings{
		MusicFile: "{Artist}/{Album}/{TrackNumber} - {Title}.{Ext}",
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

// PathMapping translates a download client's reported path prefix into the
// path where CantiNode actually sees those files — for setups where the
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
//
// internal/importer's download-progress polling isn't tunable here — it's
// keyed to how fast a download actually finishes, not a preference — but
// the health check and internal/autosearch's wanted-list sweep both are.
type TimingSettings struct {
	// HealthIntervalMinutes: background health check cadence (default 15).
	HealthIntervalMinutes int `yaml:"health_interval_minutes,omitempty" json:"healthIntervalMinutes"`
	// WantedSearchIntervalMinutes: how often internal/autosearch sweeps
	// monitored artists' wanted albums (default 1440 = 24h). Manual "Search
	// releases" is unaffected either way.
	WantedSearchIntervalMinutes int `yaml:"wanted_search_interval_minutes,omitempty" json:"wantedSearchIntervalMinutes"`
}

func (t TimingSettings) HealthInterval() time.Duration {
	if t.HealthIntervalMinutes > 0 {
		return time.Duration(t.HealthIntervalMinutes) * time.Minute
	}
	return 15 * time.Minute
}

func (t TimingSettings) WantedSearchInterval() time.Duration {
	if t.WantedSearchIntervalMinutes > 0 {
		return time.Duration(t.WantedSearchIntervalMinutes) * time.Minute
	}
	return 24 * time.Hour
}

// MusicSettings tunes internal/musicscanner's MusicBrainz matching —
// ported from CantiNode's own original, from-scratch build (before this
// codebase was rebuilt on top of a fork of LibriNode) Config fields.
// Prowlarr/qBittorrent/SABnzbd fields from that original are deliberately
// not carried over: acquisition rides CantiNode's existing indexer/
// download-client pipeline instead of a second one.
type MusicSettings struct {
	// OrganizeOnMatch, if true, has the scanner move/rename a file
	// immediately once it's matched. Defaults to false: a first scan of an
	// existing library can match hundreds of files at once, and moving
	// files on disk is much harder to casually undo than a database row —
	// safer to require an explicit Organize (per-artist or per-file)
	// through the API once the user has seen what a scan would do.
	OrganizeOnMatch bool `yaml:"organize_on_match" json:"organizeOnMatch"`
	// MinMatchConfidence is the minimum score (0-1) internal/musicscanner's
	// fuzzy MusicBrainz search must reach to auto-accept a match; anything
	// below is left unmatched for manual review instead of guessing. Has
	// no effect on a direct MBID match (from the file's own tags) or a
	// whole-folder release match, both always accepted regardless.
	MinMatchConfidence float64 `yaml:"min_match_confidence" json:"minMatchConfidence"`
	// MusicBrainzContactEmail is included in the User-Agent CantiNode sends
	// MusicBrainz, as required by its API usage policy
	// (https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) so MB can
	// reach an operator whose instance is misbehaving instead of just
	// blocking it outright. Optional, but a well-formed User-Agent without
	// real contact info is still what most API consumers get flagged for.
	MusicBrainzContactEmail string `yaml:"musicbrainz_contact_email" json:"musicbrainzContactEmail"`
	// AudioDBAPIKey configures internal/audiodb's artist bio/image lookup.
	// Optional — an empty value (the default) falls back to TheAudioDB's
	// own public shared test key rather than "not configured", since a
	// missing bio/photo is a minor cosmetic gap, not a broken feature the
	// way an unconfigured indexer/download client would be.
	AudioDBAPIKey string `yaml:"audiodb_api_key" json:"audioDbApiKey"`
}

func defaultMusic() MusicSettings {
	return MusicSettings{
		OrganizeOnMatch:    false,
		MinMatchConfidence: 0.75,
	}
}

type Config struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	APIKey   string `yaml:"api_key"`
	LogLevel string `yaml:"log_level"` // debug, info, warn, error

	Auth    AuthSettings   `yaml:"auth,omitempty"`
	Naming  NamingSettings `yaml:"naming"`
	Music   MusicSettings  `yaml:"music,omitempty"`
	Timings TimingSettings `yaml:"timings,omitempty"`
	// PathMappingList translates client-reported download paths onto this
	// machine's filesystem (Completed Download Handling reads them).
	PathMappingList []PathMapping `yaml:"path_mappings,omitempty"`

	mu      sync.Mutex
	dataDir string
}

func defaults() *Config {
	return &Config{
		Host:     "0.0.0.0",
		Port:     7847,
		LogLevel: "info",
		Naming:   defaultNaming(),
		Music:    defaultMusic(),
	}
}

// DefaultDataDir returns the OS-appropriate data directory:
// %AppData%\CantiNode on Windows, ~/.config/cantinode on Linux.
func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	name := "cantinode"
	if runtime.GOOS == "windows" {
		name = "CantiNode"
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
	if v := os.Getenv("CANTINODE_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("CANTINODE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Port = p
		}
	}
	if v := os.Getenv("CANTINODE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("CANTINODE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
}

// FillDefaults replaces an empty template with the built-in default, so a
// partial update (or hand-edited config) can never leave the music template
// empty — and thus garbage-rendering.
func (ns *NamingSettings) FillDefaults() {
	def := defaultNaming()
	if strings.TrimSpace(ns.MusicFile) == "" {
		ns.MusicFile = def.MusicFile
	}
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

// MusicSettings returns the current music-scanning options. A config
// persisted before this field existed reads back a zero MinMatchConfidence,
// which would reject every fuzzy match outright — defaulted here rather
// than in Load, so it self-heals even for a config.yaml hand-edited back to
// 0 between restarts.
func (c *Config) MusicSettings() MusicSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.Music
	if m.MinMatchConfidence <= 0 {
		m.MinMatchConfidence = defaultMusic().MinMatchConfidence
	}
	return m
}

// SetMusic replaces the music-scanning options and persists the config.
func (c *Config) SetMusic(m MusicSettings) error {
	c.mu.Lock()
	c.Music = m
	c.mu.Unlock()
	return c.save()
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
	t.HealthIntervalMinutes = clamp(t.HealthIntervalMinutes, 5, 1440)
	t.WantedSearchIntervalMinutes = clamp(t.WantedSearchIntervalMinutes, 15, 1440)
	c.mu.Lock()
	c.Timings = t
	c.mu.Unlock()
	return c.save()
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
func (c *Config) DatabasePath() string { return filepath.Join(c.dataDir, "cantinode.db") }
func (c *Config) LogPath() string      { return filepath.Join(c.dataDir, "logs", "cantinode.log") }

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
