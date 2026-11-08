package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func baseValidCfg() *Config {
	return &Config{}
}

// TestLoadIgnoresRemovedKeys pins the "no migration code" promise: a
// config.yaml still carrying keys of removed features (retrieval, index,
// chunking, embedder, loaders, ...) must load cleanly, with every key ney
// still understands intact.
func TestLoadIgnoresRemovedKeys(t *testing.T) {
	cfg := writeTestConfig(t, `embedder:
  provider: ollama
retrieval:
  top_k: 12
  mode: hybrid
index:
  exclude: ["*.bak", "[unclosed"]
chunking:
  strategy: bogus
  target_chars: 10
  overlap_chars: 500
loaders:
  some_removed_loader:
    enabled: true
context:
  active_days: 30
`)
	if cfg.Context.ActiveDays != 30 {
		t.Fatalf("active_days = %d, want 30", cfg.Context.ActiveDays)
	}
}

// writeTestConfig points $HOME at a fresh temp dir, writes the given
// config.yaml under ~/.ney, and loads it through the real config.Load() path
// so these tests exercise the actual viper decode, not just Validate().
func writeTestConfig(t *testing.T, yaml string) *Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	neyDir := filepath.Join(dir, ".ney")
	if err := os.MkdirAll(neyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(neyDir, "config.yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

// --- context.dev_roots / context.active_days -----------------------------------

func TestValidateRejectsNegativeActiveDays(t *testing.T) {
	cfg := baseValidCfg()
	cfg.Context.ActiveDays = -1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for negative context.active_days")
	}
}

// TestValidateZeroActiveDaysAccepted guards the "unset" convention: structs built directly in tests (bypassing
// Load()'s defaulting step) leave ActiveDays at its zero value and must
// still validate — Load() is the only place that turns 0 into 14.
func TestValidateZeroActiveDaysAccepted(t *testing.T) {
	cfg := baseValidCfg()
	if err := Validate(cfg); err != nil {
		t.Fatalf("zero (unset) context.active_days should validate: %v", err)
	}
}

func TestLoadDefaultsActiveDaysTo14(t *testing.T) {
	cfg := writeTestConfig(t, "embedder:\n  provider: none\n")
	if cfg.Context.ActiveDays != 14 {
		t.Fatalf("expected default context.active_days=14, got %d", cfg.Context.ActiveDays)
	}
}

func TestLoadDevRootsDefaultsToWorkspaceIfExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0700); err != nil {
		t.Fatal(err)
	}
	neyDir := filepath.Join(dir, ".ney")
	if err := os.MkdirAll(neyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(neyDir, "config.yaml"), []byte("embedder:\n  provider: none\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(dir, "workspace")
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != want {
		t.Fatalf("expected dev_roots=[%s], got %v", want, cfg.Context.DevRoots)
	}
}

func TestLoadDevRootsDefaultsToEmptyIfWorkspaceMissing(t *testing.T) {
	cfg := writeTestConfig(t, "embedder:\n  provider: none\n")
	if len(cfg.Context.DevRoots) != 0 {
		t.Fatalf("expected empty dev_roots when ~/workspace doesn't exist, got %v", cfg.Context.DevRoots)
	}
}

func TestLoadDevRootsExpandsTilde(t *testing.T) {
	cfg := writeTestConfig(t, "context:\n  dev_roots: [\"~/code\"]\n")
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, "code")
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != want {
		t.Fatalf("expected dev_roots=[%s], got %v", want, cfg.Context.DevRoots)
	}
}

func TestLoadDevRootsExplicitValueOverridesDefault(t *testing.T) {
	cfg := writeTestConfig(t, "context:\n  dev_roots: [\"/srv/repos\"]\n")
	if len(cfg.Context.DevRoots) != 1 || cfg.Context.DevRoots[0] != "/srv/repos" {
		t.Fatalf("expected explicit dev_roots to win, got %v", cfg.Context.DevRoots)
	}
}

// TestLoadIsSilentOnFirstRun pins the invariant that keeps `ney mcp` usable:
// Load must never write to stdout or stderr. The first-run hint belongs in
// cmd/ney's loadConfig, gated on CreatedDefault and a TTY.
func TestLoadIsSilentOnFirstRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w

	cfg, loadErr := Load()

	os.Stdout, os.Stderr = origOut, origErr
	w.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}

	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if buf.Len() != 0 {
		t.Errorf("Load wrote %q — it must be silent so `ney mcp` stdout stays pure JSON-RPC", buf.String())
	}
	if !cfg.CreatedDefault {
		t.Error("CreatedDefault should be true when Load created the config file")
	}

	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if again.CreatedDefault {
		t.Error("CreatedDefault should be false on a subsequent load")
	}
}

// --- handoff / sources ----------------------------------------------------------

func TestLoadHandoffDefaults(t *testing.T) {
	cfg := writeTestConfig(t, "")
	if !cfg.Handoff.Auto {
		t.Error("Handoff.Auto = false, want true by default")
	}
	if cfg.Handoff.MaxAgeDays != 14 {
		t.Errorf("MaxAgeDays = %d, want 14", cfg.Handoff.MaxAgeDays)
	}
	if want := []string{"claude-code", "codex"}; !reflect.DeepEqual(cfg.Sources, want) {
		t.Errorf("Sources = %v, want %v", cfg.Sources, want)
	}
}

func TestLoadHandoffAutoFalse(t *testing.T) {
	cfg := writeTestConfig(t, "handoff:\n  auto: false\n  disabled_repos: [\"~/secret\"]\n")
	if cfg.Handoff.Auto {
		t.Error("Handoff.Auto = true, want false when explicitly set")
	}
	home, _ := os.UserHomeDir()
	if want := []string{filepath.Join(home, "secret")}; !reflect.DeepEqual(cfg.Handoff.DisabledRepos, want) {
		t.Errorf("DisabledRepos = %v, want %v", cfg.Handoff.DisabledRepos, want)
	}
}

func TestValidateUnknownSource(t *testing.T) {
	err := Validate(&Config{Sources: []string{"claude-code", "vim"}})
	if err == nil || !strings.Contains(err.Error(), "vim") {
		t.Fatalf("err = %v, want unknown-source error naming vim", err)
	}
	if err := Validate(&Config{Handoff: HandoffConfig{MaxAgeDays: -1}}); err == nil {
		t.Error("negative max_age_days accepted")
	}
}
