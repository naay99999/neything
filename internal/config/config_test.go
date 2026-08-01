package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAutoChunkingByFormat(t *testing.T) {
	cfg := &Config{
		Chunking: ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"txt": "paragraph"}},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAutoRejectsInvalidByFormat(t *testing.T) {
	cfg := &Config{
		Chunking: ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"txt": "invalid"}},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid by_format strategy")
	}
}

func baseValidCfg() *Config {
	return &Config{Chunking: ChunkingConfig{Strategy: "markdown"}}
}

func TestValidateIndexExclude(t *testing.T) {
	cfg := baseValidCfg()
	cfg.Index.Exclude = []string{"*.bak", "drafts-*"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("valid exclude globs should pass: %v", err)
	}
	cfg.Index.Exclude = []string{"[unclosed"}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for malformed exclude glob")
	}
}

// TestValidateOldStyleConfigStillLoads guards against regressions for
// existing installs whose config.yaml predates optional providers. Stale
// keys the current Config no longer has (chat:, max_context_chars,
// loaders.git) are silently ignored by viper — see TestLoadIgnoresRemovedKeys.
// TestLoadIgnoresRemovedProviderKeys pins the "no migration code" promise of
// the keyword-only cut: a config.yaml still carrying embedder:, reranker:,
// vector_store: and the old retrieval knobs must load cleanly, with every
// key ney still understands intact.
func TestLoadIgnoresRemovedProviderKeys(t *testing.T) {
	cfg := writeTestConfig(t, `embedder:
  provider: ollama
  model: bge-m3
  endpoint: http://localhost:11434

reranker:
  provider: cohere
  model: rerank-v3.5

vector_store:
  backend: hnsw
  hnsw:
    m: 16

retrieval:
  top_k: 12
  rerank: true
  rerank_top_k: 24
  mode: hybrid

chunking:
  strategy: markdown
  target_chars: 1200
`)
	if cfg.Retrieval.TopK != 12 {
		t.Errorf("top_k = %d, want 12", cfg.Retrieval.TopK)
	}
	if cfg.Chunking.Strategy != "markdown" || cfg.Chunking.TargetChars != 1200 {
		t.Errorf("chunking not preserved: %+v", cfg.Chunking)
	}
}

// writeTestConfig points $HOME at a fresh temp dir, writes the given
// retrieval-focused config.yaml under ~/.ney, and loads it through the real
// config.Load() path (including normalizeRetrievalMode) so these tests
// exercise the actual viper decode, not just Validate().
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

// TestLoadIgnoresRemovedKeys: configs written by older ney versions still
// contain chat:, retrieval.max_context_chars, and loaders.git — none of
// which exist in the Config struct anymore. They must load without error.
func TestLoadIgnoresRemovedKeys(t *testing.T) {
	cfg := writeTestConfig(t, `embedder:
  provider: none
chat:
  provider: claude
  model: claude-sonnet-4-6
retrieval:
  top_k: 8
  max_context_chars: 12000
loaders:
  git:
    recent_commits: 5
`)
	if cfg.Retrieval.TopK != 8 {
		t.Fatalf("expected top_k 8, got %d", cfg.Retrieval.TopK)
	}
}

// TestLoadIgnoresLegacyLoaderKeys guards against regressions for pre-md-only-
// cut installs whose config.yaml still has a loaders: section (the Config
// struct no longer has a Loaders field at all) and by_format entries for
// removed doc types — viper must silently ignore the former, and the latter
// is inert (only checked when chunking.strategy is "auto").
func TestLoadIgnoresLegacyLoaderKeys(t *testing.T) {
	cfg := writeTestConfig(t, `embedder:
  provider: none
loaders:
  some_removed_loader:
    enabled: true
    extra: eng
chunking:
  strategy: markdown
  by_format:
    some_removed_doc_type: some_removed_strategy
`)
	if cfg.Chunking.Strategy != "markdown" {
		t.Fatalf("expected chunking.strategy markdown, got %q", cfg.Chunking.Strategy)
	}
}

// --- context.dev_roots / context.active_days -----------------------------------

func TestValidateRejectsNegativeActiveDays(t *testing.T) {
	cfg := baseValidCfg()
	cfg.Context.ActiveDays = -1
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for negative context.active_days")
	}
}

// TestValidateZeroActiveDaysAccepted guards the "unset" convention shared
// with Retrieval.Mode's "": structs built directly in tests (bypassing
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
