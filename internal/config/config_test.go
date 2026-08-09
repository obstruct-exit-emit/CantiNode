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
	if cfg.Port != 7845 {
		t.Errorf("default port = %d, want 7845", cfg.Port)
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

func TestImportSettingsDefaultOnAndPersistOff(t *testing.T) {
	dir := t.TempDir()

	// First run: all Completed Download Handling options default to on.
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	is := cfg.ImportSettings()
	if !is.PackImportAll || !is.RemoveCompleted || !is.DeleteCompletedFiles {
		t.Fatalf("defaults not all on: %+v", is)
	}

	// Turning them all off must persist — not get dropped and re-defaulted.
	if err := cfg.SetImport(ImportSettings{}); err != nil {
		t.Fatalf("SetImport: %v", err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	is = reloaded.ImportSettings()
	if is.PackImportAll || is.RemoveCompleted || is.DeleteCompletedFiles {
		t.Errorf("explicit off did not persist: %+v", is)
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
	t.Setenv("LIBRINODE_PORT", "9999")
	t.Setenv("LIBRINODE_LOG_LEVEL", "debug")

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
