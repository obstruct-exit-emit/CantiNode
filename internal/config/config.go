// Package config loads CantiNode's configuration from config.yaml, with
// CANTINODE_* environment variables taking precedence over file values.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config is CantiNode's full runtime configuration. Root folders are not
// part of this struct — unlike these settings, they're runtime-editable
// library state, so they live in the database (internal/database) instead,
// the same reasoning that keeps AcerviNode's downloads out of its config.
type Config struct {
	Port     int    `yaml:"port"`
	DataDir  string `yaml:"data_dir"`
	APIKey   string `yaml:"api_key"`
	LogLevel string `yaml:"log_level"`

	// ScanIntervalHours controls how often the background scan loop
	// (cmd/cantinode) walks every root folder looking for new/changed
	// files, independent of an on-demand scan triggered through the API.
	ScanIntervalHours int `yaml:"scan_interval_hours"`

	// NamingFormat is the template internal/scanner's organizer uses to
	// rename/move a matched file. Supports {Artist}, {Album}, {Year},
	// {TrackNumber}, {DiscNumber}, {Title}, {Ext}.
	NamingFormat string `yaml:"naming_format"`

	// OrganizeOnMatch, if true, has the scanner move/rename a file
	// immediately once it's matched. Defaults to false: a first scan of an
	// existing library can match hundreds of files at once, and moving
	// files on disk is much harder to casually undo than a database row —
	// safer to require an explicit Apply (per-file or bulk) through the API
	// or UI until the user has seen what a scan would do.
	OrganizeOnMatch bool `yaml:"organize_on_match"`

	// MinMatchConfidence is the minimum score (0-1) internal/scanner's
	// fuzzy MusicBrainz search must reach to auto-accept a match; anything
	// below is left unmatched for manual review instead of guessing. Has
	// no effect on a direct MBID match (from the file's own tags), which is
	// always accepted regardless.
	MinMatchConfidence float64 `yaml:"min_match_confidence"`

	// MusicBrainzContactEmail is included in the User-Agent CantiNode sends
	// MusicBrainz, as required by its API usage policy
	// (https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting) so MB can
	// reach an operator whose instance is misbehaving instead of just
	// blocking it outright. Optional, but a well-formed User-Agent without
	// real contact info is still what most API consumers get flagged for.
	MusicBrainzContactEmail string `yaml:"musicbrainz_contact_email"`

	// ProwlarrURL/ProwlarrAPIKey configure the optional acquisition
	// pipeline's indexer search — both empty (the default) means
	// internal/acquisition's Prowlarr client is simply nil, and every
	// search/grab call reports "not configured" rather than erroring
	// confusingly against an empty URL. See ROADMAP.md.
	ProwlarrURL    string `yaml:"prowlarr_url"`
	ProwlarrAPIKey string `yaml:"prowlarr_api_key"`

	// AcerviNodeURL/AcerviNodeAPIKey configure the optional acquisition
	// pipeline's download client — same "empty means not configured"
	// treatment as the Prowlarr fields above.
	AcerviNodeURL    string `yaml:"acervinode_url"`
	AcerviNodeAPIKey string `yaml:"acervinode_api_key"`
}

var validLogLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

func defaults() *Config {
	return &Config{
		Port:               7847,
		DataDir:            "./data",
		LogLevel:           "info",
		ScanIntervalHours:  6,
		NamingFormat:       "{Artist}/{Album} ({Year})/{TrackNumber} - {Title}.{Ext}",
		OrganizeOnMatch:    false,
		MinMatchConfidence: 0.75,
	}
}

// Load reads config from path (if it exists), applies CANTINODE_*
// environment overrides, fills in an API key if one wasn't set, and
// validates the result. An empty path skips the file read and uses
// defaults plus env overrides only.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// no config file yet — defaults and env vars only
		default:
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	applyEnv(cfg)

	if cfg.APIKey == "" {
		key, err := NewAPIKey()
		if err != nil {
			return nil, fmt.Errorf("generate api key: %w", err)
		}
		cfg.APIKey = key
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("CANTINODE_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	}
	if v := os.Getenv("CANTINODE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("CANTINODE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("CANTINODE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("CANTINODE_SCAN_INTERVAL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.ScanIntervalHours = n
		}
	}
	if v := os.Getenv("CANTINODE_NAMING_FORMAT"); v != "" {
		cfg.NamingFormat = v
	}
	if v := os.Getenv("CANTINODE_ORGANIZE_ON_MATCH"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.OrganizeOnMatch = b
		}
	}
	if v := os.Getenv("CANTINODE_MIN_MATCH_CONFIDENCE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.MinMatchConfidence = f
		}
	}
	if v := os.Getenv("CANTINODE_MUSICBRAINZ_CONTACT_EMAIL"); v != "" {
		cfg.MusicBrainzContactEmail = v
	}
}

// Validate reports whether c's field values are well-formed — exported so
// the settings API (internal/api) can validate a candidate update before
// committing it, the same rules Load applies at startup.
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", c.Port)
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir must not be empty")
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid log_level %q: must be one of debug, info, warn, error", c.LogLevel)
	}
	if c.ScanIntervalHours < 1 {
		return fmt.Errorf("scan_interval_hours must be at least 1")
	}
	if c.NamingFormat == "" {
		return fmt.Errorf("naming_format must not be empty")
	}
	if c.MinMatchConfidence < 0 || c.MinMatchConfidence > 1 {
		return fmt.Errorf("min_match_confidence must be between 0 and 1")
	}
	return nil
}

// Save writes the full config back to path as YAML (0600 — it contains an
// API key), overwriting whatever was there. Comments in an existing file
// are not preserved — yaml.v3's encoder doesn't round-trip them.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// NewAPIKey generates a fresh random API key — used both to fill in a
// first run's config.yaml and by the settings API to regenerate one live.
func NewAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
