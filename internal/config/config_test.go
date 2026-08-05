package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 7847 {
		t.Errorf("Port = %d, want 7847", cfg.Port)
	}
	if cfg.APIKey == "" {
		t.Error("APIKey should be auto-generated when unset")
	}
	if cfg.NamingFormat == "" {
		t.Error("NamingFormat should have a default")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 9000\napi_key: test-key\nlog_level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000", cfg.Port)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want test-key", cfg.APIKey)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	// Fields not set in the file should still fall back to defaults.
	if cfg.ScanIntervalHours != 6 {
		t.Errorf("ScanIntervalHours = %d, want default 6", cfg.ScanIntervalHours)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 7847 {
		t.Errorf("Port = %d, want default 7847", cfg.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CANTINODE_PORT", "1234")
	t.Setenv("CANTINODE_LOG_LEVEL", "warn")
	t.Setenv("CANTINODE_ORGANIZE_ON_MATCH", "true")
	t.Setenv("CANTINODE_MIN_MATCH_CONFIDENCE", "0.5")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 1234 {
		t.Errorf("Port = %d, want 1234 from env", cfg.Port)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn from env", cfg.LogLevel)
	}
	if !cfg.OrganizeOnMatch {
		t.Error("OrganizeOnMatch should be true from env")
	}
	if cfg.MinMatchConfidence != 0.5 {
		t.Errorf("MinMatchConfidence = %v, want 0.5 from env", cfg.MinMatchConfidence)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid defaults", func(c *Config) {}, false},
		{"bad port", func(c *Config) { c.Port = 0 }, true},
		{"bad log level", func(c *Config) { c.LogLevel = "verbose" }, true},
		{"zero scan interval", func(c *Config) { c.ScanIntervalHours = 0 }, true},
		{"empty naming format", func(c *Config) { c.NamingFormat = "" }, true},
		{"confidence too high", func(c *Config) { c.MinMatchConfidence = 1.5 }, true},
		{"confidence too low", func(c *Config) { c.MinMatchConfidence = -0.1 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaults()
			cfg.APIKey = "x"
			tt.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := defaults()
	cfg.APIKey = "abc123"
	cfg.Port = 5555
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Port != 5555 || loaded.APIKey != "abc123" {
		t.Errorf("round trip mismatch: got Port=%d APIKey=%q", loaded.Port, loaded.APIKey)
	}
}

func TestNewAPIKeyIsUnique(t *testing.T) {
	a, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("NewAPIKey should not produce identical keys")
	}
	if len(a) != 64 {
		t.Errorf("len(a) = %d, want 64 (32 bytes hex-encoded)", len(a))
	}
}
