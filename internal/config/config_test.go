package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsClaudeEmbedder(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for claude embedder")
	}
}

func TestValidateAcceptsValidProviders(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidVectorStore(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "faiss"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid vector store backend")
	}
}

func TestValidateRequiresModels(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "ollama", Model: ""},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty embedder model")
	}
}

func TestValidateRerankRequiresProvider(t *testing.T) {
	cfg := &Config{
		Embedder:  EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Retrieval: RetrievalConfig{Rerank: true},
		Reranker:  RerankerConfig{Provider: "invalid", Model: "x"},
		Chunking:  ChunkingConfig{Strategy: "markdown"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid reranker provider")
	}
}

func TestValidateAutoChunkingByFormat(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"txt": "paragraph"}},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAutoRejectsInvalidByFormat(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"txt": "invalid"}},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid by_format strategy")
	}
}

func TestFetchKUsesRerankTopK(t *testing.T) {
	cfg := &Config{
		Retrieval: RetrievalConfig{TopK: 8, RerankTopK: 30},
	}
	if got := FetchK(cfg, 8); got != 30 {
		t.Fatalf("expected fetchK 30, got %d", got)
	}
}

func baseValidCfg() *Config {
	return &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
}

func TestValidateEmbedderOptional(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantErr  bool
	}{
		{"empty provider skips validation", "", false},
		{"none provider skips validation", "none", false},
		{"claude always rejected", "claude", true},
		{"unknown provider rejected", "bogus", true},
		{"valid provider requires model", "ollama", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseValidCfg()
			cfg.Embedder.Provider = tt.provider
			if tt.provider != "" && tt.provider != "none" && tt.provider != "claude" {
				cfg.Embedder.Model = "some-model"
			} else {
				cfg.Embedder.Model = ""
			}
			err := Validate(cfg)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for embedder provider %q", tt.provider)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for embedder provider %q: %v", tt.provider, err)
			}
		})
	}
}

func TestValidateEmbedderNoneOrEmptyIgnoresMissingModel(t *testing.T) {
	for _, provider := range []string{"", "none"} {
		cfg := baseValidCfg()
		cfg.Embedder.Provider = provider
		cfg.Embedder.Model = ""
		if err := Validate(cfg); err != nil {
			t.Fatalf("provider %q: unexpected error: %v", provider, err)
		}
	}
}

func TestValidateEmbedderConfiguredRequiresModel(t *testing.T) {
	cfg := baseValidCfg()
	cfg.Embedder.Provider = "ollama"
	cfg.Embedder.Model = ""
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error: configured embedder provider requires a model")
	}
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

func TestHasEmbedder(t *testing.T) {
	cases := []struct {
		provider string
		want     bool
	}{
		{"", false},
		{"none", false},
		{"ollama", true},
		{"claude", true},
	}
	for _, c := range cases {
		cfg := &Config{Embedder: EmbedderConfig{Provider: c.provider}}
		if got := cfg.HasEmbedder(); got != c.want {
			t.Errorf("HasEmbedder() provider=%q = %v, want %v", c.provider, got, c.want)
		}
	}
}

func TestNewEmbedderNoneReturnsNilNil(t *testing.T) {
	for _, provider := range []string{"", "none"} {
		cfg := &Config{Embedder: EmbedderConfig{Provider: provider}}
		emb, err := NewEmbedder(cfg)
		if err != nil {
			t.Fatalf("provider %q: unexpected error: %v", provider, err)
		}
		if emb != nil {
			t.Fatalf("provider %q: expected nil embedder, got %v", provider, emb)
		}
	}
}

func TestNewEmbedderConfiguredButUnbuildableErrors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &Config{Embedder: EmbedderConfig{Provider: "openai", Model: "text-embedding-3-small"}}
	if _, err := NewEmbedder(cfg); err == nil {
		t.Fatal("expected error: openai embedder configured without API key")
	}
}

// TestValidateOldStyleConfigStillLoads guards against regressions for
// existing installs whose config.yaml predates optional providers. Stale
// keys the current Config no longer has (chat:, max_context_chars,
// loaders.git) are silently ignored by viper — see TestLoadIgnoresRemovedKeys.
func TestValidateOldStyleConfigStillLoads(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3", Endpoint: "http://localhost:11434"},
		Retrieval:   RetrievalConfig{TopK: 8, Mode: "auto"},
		Chunking:    ChunkingConfig{Strategy: "markdown", TargetChars: 1200, OverlapChars: 150},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("old-style config should still validate: %v", err)
	}
	if !cfg.HasEmbedder() {
		t.Fatal("old-style config should report the embedder configured")
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

func TestLoadRetrievalModeLegacyHybridTrue(t *testing.T) {
	cfg := writeTestConfig(t, "retrieval:\n  hybrid: true\n")
	if cfg.Retrieval.Mode != "hybrid" {
		t.Fatalf("hybrid: true should normalize to mode=hybrid, got %q", cfg.Retrieval.Mode)
	}
	if !cfg.Retrieval.Hybrid {
		t.Fatal("legacy Hybrid compat field should be true")
	}
}

func TestLoadRetrievalModeLegacyHybridFalse(t *testing.T) {
	cfg := writeTestConfig(t, "retrieval:\n  hybrid: false\n")
	if cfg.Retrieval.Mode != "auto" {
		t.Fatalf("hybrid: false should normalize to mode=auto, got %q", cfg.Retrieval.Mode)
	}
	if cfg.Retrieval.Hybrid {
		t.Fatal("legacy Hybrid compat field should be false")
	}
}

func TestLoadRetrievalModeMissingDefaultsAuto(t *testing.T) {
	cfg := writeTestConfig(t, "retrieval:\n  top_k: 8\n")
	if cfg.Retrieval.Mode != "auto" {
		t.Fatalf("missing hybrid/mode should default to auto, got %q", cfg.Retrieval.Mode)
	}
}

func TestLoadRetrievalModeExplicitStringWins(t *testing.T) {
	cfg := writeTestConfig(t, "retrieval:\n  mode: keyword\n  hybrid: true\n")
	if cfg.Retrieval.Mode != "keyword" {
		t.Fatalf("explicit mode should win over legacy hybrid, got %q", cfg.Retrieval.Mode)
	}
}

func TestLoadRetrievalModeInvalidRejected(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	neyDir := filepath.Join(dir, ".ney")
	if err := os.MkdirAll(neyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(neyDir, "config.yaml"), []byte("retrieval:\n  mode: bogus\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject invalid retrieval.mode via Validate")
	}
}

func TestValidateRejectsInvalidRetrievalMode(t *testing.T) {
	cfg := baseValidCfg()
	cfg.Retrieval.Mode = "bogus"
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid retrieval mode")
	}
}

func TestValidateAcceptsAllRetrievalModes(t *testing.T) {
	for _, mode := range []string{"", "auto", "semantic", "keyword", "hybrid"} {
		cfg := baseValidCfg()
		cfg.Retrieval.Mode = mode
		if err := Validate(cfg); err != nil {
			t.Fatalf("mode %q should be valid: %v", mode, err)
		}
	}
}
