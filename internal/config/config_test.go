package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFirstRunCreatesConfigWithAPIKey(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey == "" {
		t.Error("expected a generated API key on first run")
	}
	if cfg.Port != 7847 {
		t.Errorf("default port = %d, want 7847", cfg.Port)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("config.yaml not persisted: %v", err)
	}

	// Second load must reuse the same key, not regenerate.
	cfg2, err := Load(dir)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if cfg2.APIKey != cfg.APIKey {
		t.Error("API key changed between loads")
	}
}

// TestAuthUserMigrationAndManagement: a legacy single-account config migrates
// into the user list as the default; users can be added, promoted, and
// removed — but never the default.
func TestAuthUserMigrationAndManagement(t *testing.T) {
	dir := t.TempDir()
	legacy := "auth:\n  username: alice\n  password_hash: pbkdf2-sha256$1$aa$bb\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := cfg.AuthSettings()
	if !a.Enabled() || len(a.Users) != 1 || a.Users[0].Username != "alice" ||
		!a.Users[0].Default || a.Users[0].PasswordHash != "pbkdf2-sha256$1$aa$bb" {
		t.Fatalf("migrated auth = %+v", a)
	}
	if a.Username != "" {
		t.Error("legacy username field should be cleared after migration")
	}

	// Add a second user; alice stays default.
	if err := cfg.AddUser("bob", "hash-b", RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddUser("Bob", "hash-b2", RoleMember); err == nil {
		t.Error("case-insensitive duplicate username should be rejected")
	}
	// The default cannot be removed.
	if err := cfg.RemoveUser("alice"); err == nil {
		t.Error("removing the default user should fail")
	}
	// Promote bob, then alice becomes removable.
	if err := cfg.SetDefaultUser("bob"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.RemoveUser("alice"); err != nil {
		t.Fatalf("removing ex-default alice: %v", err)
	}

	// Everything survives a reload.
	cfg2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a = cfg2.AuthSettings()
	if len(a.Users) != 1 || a.Users[0].Username != "bob" || !a.Users[0].Default {
		t.Fatalf("reloaded users = %+v", a.Users)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CANTINODE_PORT", "9999")
	t.Setenv("CANTINODE_LOG_LEVEL", "debug")

	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999 from env", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug from env", cfg.LogLevel)
	}
}

// TestSetTimingsNormalizesWantedSearchFields: a garbage mode or time-of-day
// degrades to the default rather than persisting nonsense that would
// silently never fire (or panic parsing it back out later).
// TestTagWriteSettingsDefaultAllEnabled confirms a fresh install's
// TagWriteSettings has nothing disabled — every tag field gets written —
// without needing any self-heal logic the way MusicSettings.MinMatchConfidence
// does, since TagWriteSettings' own negative "Disable*" polarity already
// makes the Go zero value the correct default (see that type's own doc
// comment).
func TestTagWriteSettingsDefaultAllEnabled(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.TagWriteSettings()
	if got != (TagWriteSettings{}) {
		t.Errorf("TagWriteSettings() = %+v, want the zero value (nothing disabled)", got)
	}
}

// TestTagWriteSettingsPredatingConfigStillAllEnabled is the regression
// test for the exact scenario TagWriteSettings' negative polarity exists
// to handle: a config.yaml written before this section existed at all (no
// tag_write key whatsoever, not even an empty one) must still read back as
// "nothing disabled" on load — not silently disable every tag field the
// way positive "Enabled" flags would have (see TagWriteSettings' own doc
// comment).
func TestTagWriteSettingsPredatingConfigStillAllEnabled(t *testing.T) {
	dir := t.TempDir()
	raw := "host: 0.0.0.0\nport: 7847\napi_key: test-key\nnaming:\n  music_file: \"{Artist}/{Album}/{Title}.{Ext}\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.TagWriteSettings()
	if got != (TagWriteSettings{}) {
		t.Errorf("TagWriteSettings() on a config predating this section = %+v, want the zero value (nothing disabled)", got)
	}
}

// TestSetTagWritePersistsAcrossReload confirms an explicit choice to
// disable specific fields actually round-trips through a save+reload
// (simulating a server restart), and that fields never touched stay
// enabled alongside the ones deliberately disabled.
func TestSetTagWritePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := TagWriteSettings{DisableGenre: true, DisableComposer: true}
	if err := cfg.SetTagWrite(want); err != nil {
		t.Fatalf("SetTagWrite: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reloaded.TagWriteSettings()
	if got != want {
		t.Errorf("TagWriteSettings() after reload = %+v, want %+v", got, want)
	}
}

// TestSetMusicPersistsAcrossReload covers MusicBrainzBaseURL specifically
// (the self-hosted-mirror override — see MusicSettings' own doc comment)
// alongside its siblings, none of which had dedicated persistence
// coverage before.
func TestSetMusicPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := MusicSettings{
		OrganizeOnMatch:         true,
		MinMatchConfidence:      0.6,
		AutoMatchConfidence:     0.9,
		MusicBrainzContactEmail: "operator@example.com",
		AudioDBAPIKey:           "my-key",
		MusicBrainzBaseURL:      "https://mirror.example.com/ws/2",
	}
	if err := cfg.SetMusic(want); err != nil {
		t.Fatalf("SetMusic: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.MusicSettings(); got != want {
		t.Errorf("MusicSettings() after reload = %+v, want %+v", got, want)
	}
}

func TestSetTimingsNormalizesWantedSearchFields(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := cfg.SetTimings(TimingSettings{
		WantedSearchMode:      "hourly", // not a real mode
		WantedSearchTimeOfDay: "not-a-time",
	}); err != nil {
		t.Fatalf("SetTimings: %v", err)
	}
	got := cfg.TimingSettings()
	if got.WantedSearchMode != "" {
		t.Errorf("WantedSearchMode = %q, want normalized to empty (default)", got.WantedSearchMode)
	}
	if got.WantedSearchTimeOfDay != "" {
		t.Errorf("WantedSearchTimeOfDay = %q, want normalized to empty (default)", got.WantedSearchTimeOfDay)
	}

	// A valid interval-mode setting round-trips unchanged.
	if err := cfg.SetTimings(TimingSettings{
		WantedSearchMode:      WantedSearchModeInterval,
		WantedSearchTimeOfDay: "23:45",
	}); err != nil {
		t.Fatalf("SetTimings: %v", err)
	}
	got = cfg.TimingSettings()
	if got.WantedSearchMode != WantedSearchModeInterval {
		t.Errorf("WantedSearchMode = %q, want %q", got.WantedSearchMode, WantedSearchModeInterval)
	}
	if got.WantedSearchTimeOfDay != "23:45" {
		t.Errorf("WantedSearchTimeOfDay = %q, want 23:45", got.WantedSearchTimeOfDay)
	}
}
