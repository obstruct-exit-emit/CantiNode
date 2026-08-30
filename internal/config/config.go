// Package config loads and persists CantiNode's server configuration.
//
// Precedence (highest wins): environment variables (CANTINODE_*),
// values in <dataDir>/config.yaml, built-in defaults. The config file is
// created with defaults (including a freshly generated API key) on first run.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	// pass, same as {Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}.
	MusicFile string `yaml:"music_file" json:"musicFile"`
	// DisableDiscNumberForSingleDisc, when true, drops {DiscNumber} from
	// the naming template for a release with only one disc — and, when
	// {DiscNumber} is the only placeholder in its own path segment (a
	// dedicated "CD{DiscNumber}" folder, say), drops that whole segment
	// rather than leaving a bare "CD" behind. A {DiscNumber} sharing a
	// segment with something else (most commonly the filename itself,
	// e.g. "{DiscNumber}-{TrackNumber} - {Title}") only loses the
	// placeholder, never the segment, so nothing essential ever
	// disappears. false (the default) keeps today's behavior: every
	// release, single-disc included, gets its real disc number rendered
	// wherever the template asks for one — an opt-out, not an opt-in, so
	// nothing changes for an existing library unless asked for.
	DisableDiscNumberForSingleDisc bool `yaml:"disable_disc_number_for_single_disc" json:"disableDiscNumberForSingleDisc"`
}

func defaultNaming() NamingSettings {
	return NamingSettings{
		MusicFile: "{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}",
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
	// LibraryView/AlbumsView are this account's own remembered Grid/Compact/
	// List choice for the main artist library and an artist's Albums
	// section, respectively — kept separate since one person may want the
	// library dense (List) but an individual artist's albums visual
	// (Grid). Empty means "grid", the default for both. Per-account rather
	// than per-browser (contrast web/src/theme.ts's theme preference,
	// deliberately per-browser) since CantiNode's accounts are commonly
	// shared across devices by the same person.
	LibraryView string `yaml:"library_view,omitempty" json:"libraryView,omitempty"`
	AlbumsView  string `yaml:"albums_view,omitempty" json:"albumsView,omitempty"`
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

// Wanted-search schedule modes — see TimingSettings.WantedSearchMode.
const (
	WantedSearchModeInterval = "interval"
	WantedSearchModeDaily    = "daily"
)

// TimingSettings tunes the background loops. Zero values mean "use the
// default", so existing configs stay on defaults and the file only records
// deliberate choices. Changes apply on the next server start.
//
// internal/importer's download-progress polling isn't tunable here — it's
// keyed to how fast a download actually finishes, not a preference — but
// the health check, internal/autosearch's wanted-list sweep, and
// internal/discoveryrefresh's discography sweep all are.
type TimingSettings struct {
	// HealthIntervalMinutes: background health check cadence (default 15).
	HealthIntervalMinutes int `yaml:"health_interval_minutes,omitempty" json:"healthIntervalMinutes"`

	// WantedSearchMode picks how the wanted-list sweep is scheduled: "daily"
	// (the default — once a day at WantedSearchTimeOfDay) or "interval"
	// (every WantedSearchIntervalMinutes, the only mode before this
	// existed). The two are mutually exclusive rather than both active —
	// running both invites "which one wins tonight" ambiguity for no real
	// benefit, since wanting a fixed daily time and wanting a tighter
	// sub-daily cadence are different asks in the first place.
	WantedSearchMode string `yaml:"wanted_search_mode,omitempty" json:"wantedSearchMode"`
	// WantedSearchIntervalMinutes: sweep cadence when WantedSearchMode is
	// "interval" (default 1440 = 24h).
	WantedSearchIntervalMinutes int `yaml:"wanted_search_interval_minutes,omitempty" json:"wantedSearchIntervalMinutes"`
	// WantedSearchTimeOfDay: "HH:MM" (24-hour, server-local time) the sweep
	// fires at when WantedSearchMode is "daily" (default "03:00" — a quiet
	// overnight hour, not tied to when you're actually using the app).
	WantedSearchTimeOfDay string `yaml:"wanted_search_time_of_day,omitempty" json:"wantedSearchTimeOfDay"`

	// DiscographyRefreshIntervalMinutes: how often every monitored artist's
	// (and tracked series') own discography is re-cached from MusicBrainz,
	// so a new release lands in Missing without a manual "Refresh
	// metadata" click (default 1440 = 24h, matching Lidarr's own default
	// "Refresh Artist" task interval). A plain interval, not the fancier
	// daily-at-time-of-day mode WantedSearchMode has — no evidence yet
	// this needs that extra complexity.
	DiscographyRefreshIntervalMinutes int `yaml:"discography_refresh_interval_minutes,omitempty" json:"discographyRefreshIntervalMinutes"`
}

func (t TimingSettings) HealthInterval() time.Duration {
	if t.HealthIntervalMinutes > 0 {
		return time.Duration(t.HealthIntervalMinutes) * time.Minute
	}
	return 15 * time.Minute
}

// WantedSearchInterval is the "interval" mode's own cadence — exported
// separately from WantedSearchNextRun since internal/autosearch's tests
// exercise it directly without needing a whole schedule mode dance.
func (t TimingSettings) WantedSearchInterval() time.Duration {
	if t.WantedSearchIntervalMinutes > 0 {
		return time.Duration(t.WantedSearchIntervalMinutes) * time.Minute
	}
	return 24 * time.Hour
}

// DiscographyRefreshInterval is internal/discoveryrefresh's own sweep
// cadence — a plain interval, no daily/interval mode split (see
// DiscographyRefreshIntervalMinutes's own doc comment).
func (t TimingSettings) DiscographyRefreshInterval() time.Duration {
	if t.DiscographyRefreshIntervalMinutes > 0 {
		return time.Duration(t.DiscographyRefreshIntervalMinutes) * time.Minute
	}
	return 24 * time.Hour
}

// wantedSearchTimeOfDay returns the configured daily-mode fire time,
// falling back to 03:00 when unset or unparseable (SetTimings already
// normalizes a bad value back to "" before it's ever persisted, but a
// config.yaml hand-edited to something invalid should still degrade
// gracefully rather than panic or silently never fire).
func (t TimingSettings) wantedSearchTimeOfDay() (hour, minute int) {
	if h, m, ok := parseHHMM(t.WantedSearchTimeOfDay); ok {
		return h, m
	}
	return 3, 0
}

// WantedSearchNextRun returns the next time the wanted-list sweep should
// fire, computed fresh from now: now+interval in "interval" mode, or the
// next occurrence (today if it hasn't passed yet, otherwise tomorrow) of
// the configured time of day in "daily" mode (the default).
func (t TimingSettings) WantedSearchNextRun(now time.Time) time.Time {
	if t.WantedSearchMode == WantedSearchModeInterval {
		return now.Add(t.WantedSearchInterval())
	}
	hour, minute := t.wantedSearchTimeOfDay()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// parseHHMM parses a "HH:MM" 24-hour time-of-day string.
func parseHHMM(s string) (hour, minute int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
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
	// AutoMatchConfidence is the minimum name-similarity score (0-1) the
	// unmatched-files page's "Auto-match" action needs before it pre-fills
	// the artist/album dropdowns for the user — a separate, generally
	// stricter threshold from MinMatchConfidence: that one gates an
	// automatic scan silently committing a match, this one only gates
	// whether a dropdown gets pre-selected for a human to review (and
	// change) before anything is even proposed, let alone applied. Doesn't
	// apply to the version dropdown, which is picked by file-count
	// closeness instead of name similarity.
	AutoMatchConfidence float64 `yaml:"auto_match_confidence" json:"autoMatchConfidence"`
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
	// MusicBrainzBaseURL points CantiNode at a MusicBrainz-API-compatible
	// server other than the real musicbrainz.org — a self-hosted mirror
	// the operator runs and controls themselves (see
	// musicbrainz.NewClientWithBaseURL's own doc comment), not a way to
	// get free speed from someone else's infrastructure: CantiNode has no
	// bundled or recommended mirror to point this at, this is purely the
	// knob for an operator who's stood up their own. Optional — empty (the
	// default) uses the real musicbrainz.org. Applied at startup only
	// (internal/api/router.go's own construction of the musicbrainz.Client)
	// — unlike MinMatchConfidence/OrganizeOnMatch, changing it needs a
	// restart, the same as MusicBrainzContactEmail/AudioDBAPIKey right
	// above, since none of these reach into an already-constructed
	// client's own live state the way Scanner.UpdateSettings does.
	MusicBrainzBaseURL string `yaml:"musicbrainz_base_url" json:"musicbrainzBaseUrl"`
}

func defaultMusic() MusicSettings {
	return MusicSettings{
		OrganizeOnMatch:     false,
		MinMatchConfidence:  0.75,
		AutoMatchConfidence: 0.85,
	}
}

// TagWriteSettings controls which of tagwriter.Tags' fields "Write tags"
// actually embeds into a file — Settings → Music → "Tags to write" in the
// UI. Every field here is a "Disable" flag, not an "Enable" one, on
// purpose: a bool's zero value is false, and false has to be the safe,
// default behavior for a config.yaml that predates this section entirely
// (unmarshaling a missing YAML key just leaves the Go field at its zero
// value) — with positive "Enabled" flags instead, that same old config
// would read back as "disable everything," indistinguishable from a user
// who genuinely unchecked every single tag, and unlike
// MusicSettings.MinMatchConfidence's own self-heal (an obviously-invalid
// zero that's safe to override), disabling every tag field is a real,
// if unusual, intentional choice this package must not silently reverse.
// Negative polarity here sidesteps the ambiguity outright: nothing
// disabled (the zero value) already means everything gets written,
// whether that's because a fresh install never touched this section or
// because an existing config.yaml simply predates it.
type TagWriteSettings struct {
	DisableTitle                     bool `yaml:"disable_title" json:"disableTitle"`
	DisableArtist                    bool `yaml:"disable_artist" json:"disableArtist"`
	DisableAlbumArtist               bool `yaml:"disable_album_artist" json:"disableAlbumArtist"`
	DisableAlbum                     bool `yaml:"disable_album" json:"disableAlbum"`
	DisableTrackNumber               bool `yaml:"disable_track_number" json:"disableTrackNumber"`
	DisableDiscNumber                bool `yaml:"disable_disc_number" json:"disableDiscNumber"`
	DisableDate                      bool `yaml:"disable_date" json:"disableDate"`
	DisableTrackTotal                bool `yaml:"disable_track_total" json:"disableTrackTotal"`
	DisableDiscTotal                 bool `yaml:"disable_disc_total" json:"disableDiscTotal"`
	DisableGenre                     bool `yaml:"disable_genre" json:"disableGenre"`
	DisableReleaseType               bool `yaml:"disable_release_type" json:"disableReleaseType"`
	DisableArtistSortName            bool `yaml:"disable_artist_sort_name" json:"disableArtistSortName"`
	DisableAlbumArtistSortName       bool `yaml:"disable_album_artist_sort_name" json:"disableAlbumArtistSortName"`
	DisableReleaseCountry            bool `yaml:"disable_release_country" json:"disableReleaseCountry"`
	DisableReleaseStatus             bool `yaml:"disable_release_status" json:"disableReleaseStatus"`
	DisableMedia                     bool `yaml:"disable_media" json:"disableMedia"`
	DisableMood                      bool `yaml:"disable_mood" json:"disableMood"`
	DisableComposer                  bool `yaml:"disable_composer" json:"disableComposer"`
	DisableCoverImage                bool `yaml:"disable_cover_image" json:"disableCoverImage"`
	DisableMusicBrainzArtistID       bool `yaml:"disable_musicbrainz_artist_id" json:"disableMusicBrainzArtistId"`
	DisableAlbumArtistID             bool `yaml:"disable_album_artist_id" json:"disableAlbumArtistId"`
	DisableMusicBrainzAlbumID        bool `yaml:"disable_musicbrainz_album_id" json:"disableMusicBrainzAlbumId"`
	DisableMusicBrainzReleaseGroupID bool `yaml:"disable_musicbrainz_release_group_id" json:"disableMusicBrainzReleaseGroupId"`
	DisableMusicBrainzRecordingID    bool `yaml:"disable_musicbrainz_recording_id" json:"disableMusicBrainzRecordingId"`
}

// defaultTagWrite is the zero value, spelled out for the same discoverable
// symmetry defaultMusic()/defaultNaming() already give every other
// settings section — nothing to actually set, since "disable nothing" (see
// TagWriteSettings' own doc comment) is already Go's zero value for every
// field here.
func defaultTagWrite() TagWriteSettings {
	return TagWriteSettings{}
}

type Config struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	APIKey   string `yaml:"api_key"`
	LogLevel string `yaml:"log_level"` // debug, info, warn, error

	Auth     AuthSettings     `yaml:"auth,omitempty"`
	Naming   NamingSettings   `yaml:"naming"`
	Music    MusicSettings    `yaml:"music,omitempty"`
	TagWrite TagWriteSettings `yaml:"tag_write,omitempty"`
	Timings  TimingSettings   `yaml:"timings,omitempty"`
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
		TagWrite: defaultTagWrite(),
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
	if m.AutoMatchConfidence <= 0 {
		m.AutoMatchConfidence = defaultMusic().AutoMatchConfidence
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

// TagWriteSettings returns which tag fields "Write tags" should embed —
// no self-heal needed here the way MusicSettings has (see
// TagWriteSettings' own doc comment on why negative polarity makes the
// zero value already correct).
func (c *Config) TagWriteSettings() TagWriteSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.TagWrite
}

// SetTagWrite replaces the tag-write field toggles and persists the config.
func (c *Config) SetTagWrite(t TagWriteSettings) error {
	c.mu.Lock()
	c.TagWrite = t
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
	t.DiscographyRefreshIntervalMinutes = clamp(t.DiscographyRefreshIntervalMinutes, 15, 1440)
	if t.WantedSearchMode != WantedSearchModeInterval {
		t.WantedSearchMode = "" // anything but "interval" normalizes to the default ("daily")
	}
	if _, _, ok := parseHHMM(t.WantedSearchTimeOfDay); !ok {
		t.WantedSearchTimeOfDay = "" // malformed input normalizes to the default (03:00) rather than erroring
	}
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

// ErrUserExists means AddUser's username already belongs to another
// account (compared case-insensitively) — the one AddUser failure that's
// a real conflict (409); every other failure (a bad role, in practice) is
// the caller's malformed request (400).
var ErrUserExists = errors.New("config: user already exists")

// AddUser appends a login account with the given role (RoleAdmin,
// RoleMember, or "" to mean RoleMember, the default for a newly added
// account) — any other value is rejected rather than silently folded into
// RoleMember (see the check below). The first account becomes the
// protected default and is always admin regardless of what's requested,
// since the default being demotable could leave an instance with no admin
// at all.
func (c *Config) AddUser(username, passwordHash, role string) error {
	// Empty means "use the default" (member); anything else must name a
	// real role. Found live: this used to silently fold any unrecognized
	// value — a typo, or a role that plain doesn't exist — into "member"
	// rather than rejecting it, so a mistyped role request looked like it
	// succeeded but silently granted a different role than asked for.
	if role != "" && role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("role must be %q or %q", RoleAdmin, RoleMember)
	}
	c.mu.Lock()
	for i := range c.Auth.Users {
		if strings.EqualFold(c.Auth.Users[i].Username, username) {
			c.mu.Unlock()
			return fmt.Errorf("%w: %q", ErrUserExists, username)
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

// validViewPref reports whether v is a recognized Grid/Compact/List choice
// — "" (meaning "use the default") is valid too, so clearing a preference
// back to default doesn't need its own separate call.
func validViewPref(v string) bool {
	return v == "" || v == "grid" || v == "compact" || v == "list"
}

// SetUserLibraryView stores username's remembered view for the main artist
// library — see UserAccount.LibraryView.
func (c *Config) SetUserLibraryView(username, view string) error {
	if !validViewPref(view) {
		return fmt.Errorf("view must be %q, %q, %q, or empty", "grid", "compact", "list")
	}
	c.mu.Lock()
	u := c.Auth.Find(username)
	if u == nil {
		c.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	u.LibraryView = view
	c.mu.Unlock()
	return c.save()
}

// SetUserAlbumsView stores username's remembered view for an artist page's
// own Albums section — see UserAccount.AlbumsView.
func (c *Config) SetUserAlbumsView(username, view string) error {
	if !validViewPref(view) {
		return fmt.Errorf("view must be %q, %q, %q, or empty", "grid", "compact", "list")
	}
	c.mu.Lock()
	u := c.Auth.Find(username)
	if u == nil {
		c.mu.Unlock()
		return fmt.Errorf("user %q not found", username)
	}
	u.AlbumsView = view
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
