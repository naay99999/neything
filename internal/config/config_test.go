package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRejectsClaudeEmbedder(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chat:     ChatConfig{Provider: "openai", Model: "gpt-4o"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for claude embedder")
	}
}

func TestValidateAcceptsValidProviders(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
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
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
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
		Chat:     ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty embedder model")
	}
}

func TestValidateRerankRequiresProvider(t *testing.T) {
	cfg := &Config{
		Embedder:  EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:      ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
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
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"pdf": "page"}},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAutoRejectsInvalidByFormat(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"pdf": "invalid"}},
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
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
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

func TestValidateChatOptional(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		wantErr  bool
	}{
		{"empty provider skips validation", "", "", false},
		{"none provider skips validation", "none", "", false},
		{"valid provider requires model", "claude", "claude-sonnet-4-6", false},
		{"configured provider missing model errors", "claude", "", true},
		{"unknown provider rejected", "bogus", "x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseValidCfg()
			cfg.Chat.Provider = tt.provider
			cfg.Chat.Model = tt.model
			err := Validate(cfg)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for chat provider %q model %q", tt.provider, tt.model)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for chat provider %q model %q: %v", tt.provider, tt.model, err)
			}
		})
	}
}

func TestValidateBothOptionalPassesTogether(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "none"},
		Chat:        ChatConfig{Provider: ""},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error with both providers unset: %v", err)
	}
}

func TestHasEmbedderHasChat(t *testing.T) {
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
		cfg := &Config{Embedder: EmbedderConfig{Provider: c.provider}, Chat: ChatConfig{Provider: c.provider}}
		if got := cfg.HasEmbedder(); got != c.want {
			t.Errorf("HasEmbedder() provider=%q = %v, want %v", c.provider, got, c.want)
		}
		if got := cfg.HasChat(); got != c.want {
			t.Errorf("HasChat() provider=%q = %v, want %v", c.provider, got, c.want)
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

func TestNewChatModelNoneReturnsNilNil(t *testing.T) {
	for _, provider := range []string{"", "none"} {
		cfg := &Config{Chat: ChatConfig{Provider: provider}}
		cm, err := NewChatModel(cfg)
		if err != nil {
			t.Fatalf("provider %q: unexpected error: %v", provider, err)
		}
		if cm != nil {
			t.Fatalf("provider %q: expected nil chat model, got %v", provider, cm)
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

func TestNewChatModelConfiguredButUnbuildableErrors(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	cfg := &Config{Chat: ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"}}
	if _, err := NewChatModel(cfg); err == nil {
		t.Fatal("expected error: claude chat configured without API key")
	}
}

// TestValidateOldStyleConfigStillLoads guards against regressions for
// existing installs whose config.yaml predates optional providers (explicit
// embedder ollama + chat claude, hybrid: false → normalized to mode: auto
// before Validate ever sees it — see TestLoadRetrievalModeLegacyHybridFalse).
func TestValidateOldStyleConfigStillLoads(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3", Endpoint: "http://localhost:11434"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Retrieval:   RetrievalConfig{TopK: 8, MaxContextChars: 12000, Mode: "auto"},
		Chunking:    ChunkingConfig{Strategy: "markdown", TargetChars: 1200, OverlapChars: 150},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("old-style config should still validate: %v", err)
	}
	if !cfg.HasEmbedder() || !cfg.HasChat() {
		t.Fatal("old-style config should report both providers configured")
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
