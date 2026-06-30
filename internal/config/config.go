package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	TopK            int  `mapstructure:"top_k"`
	MaxContextChars int  `mapstructure:"max_context_chars"`
	Rerank          bool `mapstructure:"rerank"`
	RerankTopK      int  `mapstructure:"rerank_top_k"`
	Hybrid          bool `mapstructure:"hybrid"`
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

# embedder: used to create vectors (cannot be claude)
embedder:
  provider: ollama          # openai | gemini | ollama
  model: bge-m3
  # endpoint: http://localhost:11434   # ollama only

# chat: used to answer questions in 'ney ask'
chat:
  provider: claude          # claude | openai | gemini | ollama
  model: claude-sonnet-4-6

# retrieval settings
retrieval:
  top_k: 8
  max_context_chars: 12000
  rerank: false
  rerank_top_k: 24
  hybrid: false

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
		fmt.Fprintf(os.Stderr, "Edit it to configure your providers and API keys.\n\n")
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

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Validate(cfg *Config) error {
	if cfg.Embedder.Provider == "claude" {
		return fmt.Errorf("Claude does not provide an embedding API.\nSet embedder.provider to: openai, gemini, ollama, or lmstudio")
	}
	validEmbed := map[string]bool{"openai": true, "gemini": true, "ollama": true, "lmstudio": true}
	if !validEmbed[cfg.Embedder.Provider] {
		return fmt.Errorf("unknown embedder provider %q (valid: openai, gemini, ollama, lmstudio)", cfg.Embedder.Provider)
	}
	validChat := map[string]bool{"claude": true, "openai": true, "gemini": true, "ollama": true, "lmstudio": true}
	if !validChat[cfg.Chat.Provider] {
		return fmt.Errorf("unknown chat provider %q (valid: claude, openai, gemini, ollama, lmstudio)", cfg.Chat.Provider)
	}
	if cfg.Embedder.Model == "" {
		return fmt.Errorf("embedder.model is required")
	}
	if cfg.Chat.Model == "" {
		return fmt.Errorf("chat.model is required")
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
	return nil
}

func apiKey(envVar string) string {
	return os.Getenv(envVar)
}

func NewEmbedder(cfg *Config) (embed.Embedder, error) {
	switch cfg.Embedder.Provider {
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

func NewChatModel(cfg *Config) (chat.ChatModel, error) {
	switch cfg.Chat.Provider {
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
