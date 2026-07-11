package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naay99999/neything/internal/chat"
	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/rerank"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
	"github.com/spf13/viper"
)

type Config struct {
	Embedder    EmbedderConfig    `mapstructure:"embedder"`
	Chat        ChatConfig        `mapstructure:"chat"`
	Retrieval   RetrievalConfig   `mapstructure:"retrieval"`
	Reranker    RerankerConfig    `mapstructure:"reranker"`
	Chunking    ChunkingConfig    `mapstructure:"chunking"`
	Loaders     LoadersConfig     `mapstructure:"loaders"`
	VectorStore VectorStoreConfig `mapstructure:"vector_store"`
	Telemetry   bool              `mapstructure:"telemetry"`
}

type EmbedderConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	Endpoint string `mapstructure:"endpoint"`
}

type ChatConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	Endpoint string `mapstructure:"endpoint"`
}

type RetrievalConfig struct {
	TopK            int    `mapstructure:"top_k"`
	MaxContextChars int    `mapstructure:"max_context_chars"`
	Rerank          bool   `mapstructure:"rerank"`
	RerankTopK      int    `mapstructure:"rerank_top_k"`
	// Mode is the canonical retrieval mode: auto | semantic | keyword |
	// hybrid. Set by Load() via normalizeRetrievalMode, which also accepts
	// the legacy "hybrid" YAML key (bool or string) for installs that
	// predate auto mode. New code should read Mode, not Hybrid.
	Mode string `mapstructure:"mode"`
	// Hybrid is a legacy compatibility field derived from Mode (true only
	// when Mode == "hybrid"). It intentionally excludes "hybrid" from
	// mapstructure decoding (tag "-") because that YAML key can be either a
	// bool (legacy) or a string in the wild; normalizeRetrievalMode handles
	// both by reading the raw value directly. Kept only so existing callers
	// that still branch on it keep compiling — do not set it directly.
	Hybrid bool `mapstructure:"-"`
}

type RerankerConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	Endpoint string `mapstructure:"endpoint"`
}

type ChunkingConfig struct {
	Strategy      string            `mapstructure:"strategy"`
	TargetChars   int               `mapstructure:"target_chars"`
	OverlapChars  int               `mapstructure:"overlap_chars"`
	TargetTokens  int               `mapstructure:"target_tokens"`
	OverlapTokens int               `mapstructure:"overlap_tokens"`
	ByFormat      map[string]string `mapstructure:"by_format"`
}

type LoadersConfig struct {
	Git GitLoaderConfig `mapstructure:"git"`
	OCR OCRConfig       `mapstructure:"ocr"`
}

type GitLoaderConfig struct {
	RecentCommits int `mapstructure:"recent_commits"`
}

type OCRConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Lang         string `mapstructure:"lang"`
	TesseractCmd string `mapstructure:"tesseract_cmd"`
	PdftoppmCmd  string `mapstructure:"pdftoppm_cmd"`
	MinChars     int    `mapstructure:"min_chars"`
}

type VectorStoreConfig struct {
	Backend string     `mapstructure:"backend"`
	HNSW    HNSWConfig `mapstructure:"hnsw"`
}

type HNSWConfig struct {
	M              int `mapstructure:"m"`
	EfConstruction int `mapstructure:"ef_construction"`
	EfSearch       int `mapstructure:"ef_search"`
}

const defaultConfig = `# Ney configuration (~/.ney/config.yaml)

# Tip: run 'ney init' to enable semantic search and 'ney ask' interactively.
# Without an embedder/chat provider, ney still indexes and searches by
# keyword (FTS) — just run 'ney index' and 'ney search' to try it now.

# embedder: used to create vectors for semantic search (cannot be claude)
embedder:
  provider: none            # none | openai | gemini | ollama | lmstudio
  # model: bge-m3
  # endpoint: http://localhost:11434   # ollama / lmstudio (LM Studio default: http://localhost:1234)

# chat: used to answer questions in 'ney ask'
chat:
  provider: none            # none | claude | openai | gemini | ollama | lmstudio
  # model: claude-sonnet-4-6
  # endpoint: http://localhost:1234    # ollama / lmstudio only

# retrieval settings
retrieval:
  top_k: 8
  max_context_chars: 12000
  rerank: false
  rerank_top_k: 24
  mode: auto                # auto | semantic | keyword | hybrid
  # legacy installs may still have "hybrid: true/false" instead of "mode" —
  # true maps to hybrid, false maps to auto; an explicit "mode" always wins

# reranker: used when retrieval.rerank is true
reranker:
  provider: cohere          # cohere | jina | ollama
  model: rerank-v3.5
  # endpoint: http://localhost:11434   # ollama/local only

# chunking settings
chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer | page
  target_chars: 1200
  overlap_chars: 150
  target_tokens: 300          # tokenizer strategy only (~4 chars/token)
  overlap_tokens: 50
  # by_format used when strategy: auto
  # by_format:
  #   md: markdown
  #   pdf: page
  #   docx: paragraph

# loader options
loaders:
  git:
    recent_commits: 0       # index recent git commits (0 = disabled)
  ocr:
    enabled: false
    lang: eng
    min_chars: 32

# vector store backend
vector_store:
  backend: brute          # brute | hnsw
  hnsw:
    m: 16
    ef_construction: 200
    ef_search: 50

# privacy — off by default
telemetry: false

# API keys — set via env vars (recommended) or here:
# ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY
`

func NeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ney")
}

func DBPath() string      { return filepath.Join(NeyDir(), "index.db") }
func VectorsPath() string { return filepath.Join(NeyDir(), "vectors.bin") }
func HNSWPath() string    { return filepath.Join(NeyDir(), "vectors.hnsw") }
func ConfigPath() string  { return filepath.Join(NeyDir(), "config.yaml") }

const metaVectorStoreBackend = "vector_store_backend"

func Load() (*Config, error) {
	cfgPath := ConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.MkdirAll(NeyDir(), 0700); err != nil {
			return nil, fmt.Errorf("create ~/.ney: %w", err)
		}
		if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0600); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Created default config at %s\n", cfgPath)
		fmt.Fprintf(os.Stderr, "Run `ney init` for interactive setup, or edit the file to configure providers.\n\n")
	}

	v := viper.New()
	v.SetConfigFile(cfgPath)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// apply defaults
	if cfg.Retrieval.TopK == 0 {
		cfg.Retrieval.TopK = 8
	}
	if cfg.Retrieval.MaxContextChars == 0 {
		cfg.Retrieval.MaxContextChars = 12000
	}
	if cfg.Chunking.Strategy == "" {
		cfg.Chunking.Strategy = "markdown"
	}
	if cfg.Chunking.TargetChars == 0 {
		cfg.Chunking.TargetChars = 1200
	}
	if cfg.Chunking.OverlapChars == 0 {
		cfg.Chunking.OverlapChars = 150
	}
	if cfg.Chunking.TargetTokens == 0 {
		cfg.Chunking.TargetTokens = 300
	}
	if cfg.Chunking.OverlapTokens == 0 {
		cfg.Chunking.OverlapTokens = 50
	}
	if cfg.Loaders.OCR.Lang == "" {
		cfg.Loaders.OCR.Lang = "eng"
	}
	if cfg.Loaders.OCR.MinChars == 0 {
		cfg.Loaders.OCR.MinChars = 32
	}
	if cfg.Loaders.OCR.TesseractCmd == "" {
		cfg.Loaders.OCR.TesseractCmd = "tesseract"
	}
	if cfg.Loaders.OCR.PdftoppmCmd == "" {
		cfg.Loaders.OCR.PdftoppmCmd = "pdftoppm"
	}
	if cfg.VectorStore.Backend == "" {
		cfg.VectorStore.Backend = "brute"
	}
	if cfg.VectorStore.HNSW.M == 0 {
		cfg.VectorStore.HNSW.M = 16
	}
	if cfg.VectorStore.HNSW.EfConstruction == 0 {
		cfg.VectorStore.HNSW.EfConstruction = 200
	}
	if cfg.VectorStore.HNSW.EfSearch == 0 {
		cfg.VectorStore.HNSW.EfSearch = 50
	}

	cfg.Retrieval.Mode = normalizeRetrievalMode(v)
	// Legacy compat: some callers still branch on the old bool field.
	cfg.Retrieval.Hybrid = cfg.Retrieval.Mode == "hybrid"

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// normalizeRetrievalMode resolves retrieval.mode from either the canonical
// "mode" string key or the legacy "hybrid" key. Installs from before auto
// mode existed have "hybrid: false" (the old default) or "hybrid: true"
// written to their config.yaml — those still need to load. An explicit
// "mode" always wins over "hybrid". Defaults to "auto" when neither is set.
func normalizeRetrievalMode(v *viper.Viper) string {
	if v.IsSet("retrieval.mode") {
		if m := strings.ToLower(strings.TrimSpace(v.GetString("retrieval.mode"))); m != "" {
			return m
		}
	}
	if v.IsSet("retrieval.hybrid") {
		switch val := v.Get("retrieval.hybrid").(type) {
		case bool:
			if val {
				return "hybrid"
			}
			return "auto"
		case string:
			switch s := strings.ToLower(strings.TrimSpace(val)); s {
			case "true":
				return "hybrid"
			case "false", "":
				return "auto"
			default:
				// Someone wrote a mode name under the legacy key — accept it.
				return s
			}
		}
	}
	return "auto"
}

func Validate(cfg *Config) error {
	if cfg.Embedder.Provider == "claude" {
		return fmt.Errorf("Claude does not provide an embedding API.\nSet embedder.provider to: openai, gemini, ollama, or lmstudio")
	}
	if cfg.Embedder.Provider != "" && cfg.Embedder.Provider != "none" {
		validEmbed := map[string]bool{"openai": true, "gemini": true, "ollama": true, "lmstudio": true}
		if !validEmbed[cfg.Embedder.Provider] {
			return fmt.Errorf("unknown embedder provider %q (valid: none, openai, gemini, ollama, lmstudio)", cfg.Embedder.Provider)
		}
		if cfg.Embedder.Model == "" {
			return fmt.Errorf("embedder.model is required")
		}
	}
	if cfg.Chat.Provider != "" && cfg.Chat.Provider != "none" {
		validChat := map[string]bool{"claude": true, "openai": true, "gemini": true, "ollama": true, "lmstudio": true}
		if !validChat[cfg.Chat.Provider] {
			return fmt.Errorf("unknown chat provider %q (valid: none, claude, openai, gemini, ollama, lmstudio)", cfg.Chat.Provider)
		}
		if cfg.Chat.Model == "" {
			return fmt.Errorf("chat.model is required")
		}
	}
	validChunk := map[string]bool{
		"auto": true, "character": true, "paragraph": true, "markdown": true, "sentence": true, "tokenizer": true, "page": true,
	}
	if !validChunk[cfg.Chunking.Strategy] {
		return fmt.Errorf("unknown chunking strategy %q (valid: auto, character, paragraph, markdown, sentence, tokenizer, page)", cfg.Chunking.Strategy)
	}
	if cfg.Chunking.Strategy == "auto" {
		validPerFormat := map[string]bool{
			"character": true, "paragraph": true, "markdown": true, "sentence": true, "tokenizer": true, "page": true,
		}
		for docType, strat := range cfg.Chunking.ByFormat {
			if !validPerFormat[strat] {
				return fmt.Errorf("unknown chunking.by_format.%s strategy %q", docType, strat)
			}
		}
	}
	if cfg.Retrieval.Rerank {
		validRerank := map[string]bool{"cohere": true, "jina": true, "ollama": true}
		if !validRerank[cfg.Reranker.Provider] {
			return fmt.Errorf("unknown reranker provider %q (valid: cohere, jina, ollama)", cfg.Reranker.Provider)
		}
		if cfg.Reranker.Model == "" {
			return fmt.Errorf("reranker.model is required when retrieval.rerank is true")
		}
	}
	validVectorStore := map[string]bool{"brute": true, "hnsw": true}
	if !validVectorStore[cfg.VectorStore.Backend] {
		return fmt.Errorf("unknown vector_store.backend %q (valid: brute, hnsw)", cfg.VectorStore.Backend)
	}
	// "" is accepted here (treated as "auto" by callers) so structs built
	// directly in tests without setting Retrieval.Mode don't need to know
	// about this field. Load() always normalizes it to a concrete value.
	validMode := map[string]bool{"": true, "auto": true, "semantic": true, "keyword": true, "hybrid": true}
	if !validMode[cfg.Retrieval.Mode] {
		return fmt.Errorf("unknown retrieval mode %q (valid: auto, semantic, keyword, hybrid)", cfg.Retrieval.Mode)
	}
	return nil
}

// HasEmbedder reports whether an embedder provider is configured. When
// false, ney runs in keyword-only (FTS) mode — no semantic search.
func (c *Config) HasEmbedder() bool {
	return c.Embedder.Provider != "" && c.Embedder.Provider != "none"
}

// HasChat reports whether a chat provider is configured. When false, `ney
// ask` and the REPL's ask path are unavailable until `ney init` sets one up.
func (c *Config) HasChat() bool {
	return c.Chat.Provider != "" && c.Chat.Provider != "none"
}

func apiKey(envVar string) string {
	return os.Getenv(envVar)
}

// NewEmbedder builds the configured embedder. It returns (nil, nil) when no
// embedder is configured — that is a normal, supported state, not an error.
// A provider that IS configured but fails to build (e.g. missing API key)
// still returns an error.
func NewEmbedder(cfg *Config) (embed.Embedder, error) {
	switch cfg.Embedder.Provider {
	case "", "none":
		return nil, nil
	case "openai":
		key := apiKey("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		return embed.NewOpenAIEmbedder(key, cfg.Embedder.Model), nil
	case "gemini":
		key := apiKey("GEMINI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not set")
		}
		return embed.NewGeminiEmbedder(key, cfg.Embedder.Model), nil
	case "ollama":
		return embed.NewOllamaEmbedder(cfg.Embedder.Endpoint, cfg.Embedder.Model)
	case "lmstudio":
		endpoint := cfg.Embedder.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:1234"
		}
		return embed.NewOpenAICompatibleEmbedder(endpoint, cfg.Embedder.Model), nil
	default:
		return nil, fmt.Errorf("unknown embedder provider: %s", cfg.Embedder.Provider)
	}
}

// NewChatModel builds the configured chat model. It returns (nil, nil) when
// no chat provider is configured — commands that need it (ask, REPL ask)
// check for nil themselves and print a friendly hint rather than failing at
// config load time. A provider that IS configured but fails to build still
// returns an error.
func NewChatModel(cfg *Config) (chat.ChatModel, error) {
	switch cfg.Chat.Provider {
	case "", "none":
		return nil, nil
	case "claude":
		key := apiKey("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return chat.NewClaudeChatModel(key, cfg.Chat.Model), nil
	case "openai":
		key := apiKey("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		return chat.NewOpenAIChatModel(key, cfg.Chat.Model), nil
	case "gemini":
		key := apiKey("GEMINI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY not set")
		}
		return chat.NewGeminiChatModel(key, cfg.Chat.Model), nil
	case "ollama":
		return chat.NewOllamaChatModel(cfg.Chat.Endpoint, cfg.Chat.Model), nil
	case "lmstudio":
		endpoint := cfg.Chat.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:1234"
		}
		return chat.NewOpenAICompatibleChatModel(endpoint, cfg.Chat.Model), nil
	default:
		return nil, fmt.Errorf("unknown chat provider: %s", cfg.Chat.Provider)
	}
}

func NewReranker(cfg *Config) (rerank.Reranker, error) {
	if !cfg.Retrieval.Rerank {
		return nil, nil
	}
	switch cfg.Reranker.Provider {
	case "cohere":
		key := apiKey("COHERE_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("COHERE_API_KEY not set")
		}
		return rerank.NewCohereReranker(key, cfg.Reranker.Model), nil
	case "jina":
		key := apiKey("JINA_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("JINA_API_KEY not set")
		}
		return rerank.NewJinaReranker(key, cfg.Reranker.Model), nil
	case "ollama":
		return rerank.NewOllamaReranker(cfg.Reranker.Endpoint, cfg.Reranker.Model), nil
	default:
		return nil, fmt.Errorf("unknown reranker provider: %s", cfg.Reranker.Provider)
	}
}

func FetchK(cfg *Config, topK int) int {
	if topK <= 0 {
		topK = cfg.Retrieval.TopK
	}
	fetchK := topK * 3
	if cfg.Retrieval.RerankTopK > fetchK {
		fetchK = cfg.Retrieval.RerankTopK
	}
	if fetchK < 10 {
		fetchK = 10
	}
	return fetchK
}

func NewVectorStore(cfg *Config, db *store.DB, migrate bool) (vectorstore.VectorStore, error) {
	backend := cfg.VectorStore.Backend
	stored, err := db.GetMeta(metaVectorStoreBackend)
	if err != nil {
		return nil, err
	}
	if stored != "" && stored != backend {
		return nil, fmt.Errorf(
			"vector store backend mismatch: index uses %q, config says %q\nRun: ney reset && ney index <path>",
			stored, backend,
		)
	}

	opts := vectorstore.HNSWOptions{
		M:              cfg.VectorStore.HNSW.M,
		EfConstruction: cfg.VectorStore.HNSW.EfConstruction,
		EfSearch:       cfg.VectorStore.HNSW.EfSearch,
	}

	switch backend {
	case "brute":
		vs, err := vectorstore.NewBruteForceStore(VectorsPath())
		if err != nil {
			return nil, err
		}
		if stored == "" {
			_ = db.SetMeta(metaVectorStoreBackend, "brute")
		}
		return vs, nil
	case "hnsw":
		hnswPath := HNSWPath()
		brutePath := VectorsPath()
		if migrate || (fileExists(brutePath) && !fileExists(hnswPath)) {
			if err := vectorstore.ImportBruteForceToHNSW(brutePath, hnswPath, opts); err != nil {
				return nil, fmt.Errorf("migrate vectors to hnsw: %w", err)
			}
		}
		vs, err := vectorstore.NewHNSWStore(hnswPath, opts)
		if err != nil {
			return nil, err
		}
		if stored == "" {
			_ = db.SetMeta(metaVectorStoreBackend, "hnsw")
		}
		return vs, nil
	default:
		return nil, fmt.Errorf("unknown vector store backend: %s", backend)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
