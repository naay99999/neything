package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/naay/ney/internal/chat"
	"github.com/naay/ney/internal/embed"
	"github.com/spf13/viper"
)

type Config struct {
	Embedder  EmbedderConfig  `mapstructure:"embedder"`
	Chat      ChatConfig      `mapstructure:"chat"`
	Retrieval RetrievalConfig `mapstructure:"retrieval"`
	Chunking  ChunkingConfig  `mapstructure:"chunking"`
	Telemetry bool            `mapstructure:"telemetry"`
}

type EmbedderConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	Endpoint string `mapstructure:"endpoint"`
}

type ChatConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
}

type RetrievalConfig struct {
	TopK            int  `mapstructure:"top_k"`
	MaxContextChars int  `mapstructure:"max_context_chars"`
	Rerank          bool `mapstructure:"rerank"`
}

type ChunkingConfig struct {
	Strategy     string `mapstructure:"strategy"`
	TargetChars  int    `mapstructure:"target_chars"`
	OverlapChars int    `mapstructure:"overlap_chars"`
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

# chunking settings
chunking:
  strategy: markdown        # character | sentence | paragraph | markdown
  target_chars: 1200
  overlap_chars: 150

# privacy — off by default
telemetry: false

# API keys — set via env vars (recommended) or here:
# ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY
`

func NeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ney")
}

func DBPath() string    { return filepath.Join(NeyDir(), "index.db") }
func VectorsPath() string { return filepath.Join(NeyDir(), "vectors.bin") }
func ConfigPath() string  { return filepath.Join(NeyDir(), "config.yaml") }

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

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Validate(cfg *Config) error {
	if cfg.Embedder.Provider == "claude" {
		return fmt.Errorf("Claude does not provide an embedding API.\nSet embedder.provider to: openai, gemini, or ollama")
	}
	validEmbed := map[string]bool{"openai": true, "gemini": true, "ollama": true}
	if !validEmbed[cfg.Embedder.Provider] {
		return fmt.Errorf("unknown embedder provider %q (valid: openai, gemini, ollama)", cfg.Embedder.Provider)
	}
	validChat := map[string]bool{"claude": true, "openai": true, "gemini": true, "ollama": true}
	if !validChat[cfg.Chat.Provider] {
		return fmt.Errorf("unknown chat provider %q (valid: claude, openai, gemini, ollama)", cfg.Chat.Provider)
	}
	if cfg.Embedder.Model == "" {
		return fmt.Errorf("embedder.model is required")
	}
	if cfg.Chat.Model == "" {
		return fmt.Errorf("chat.model is required")
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
		endpoint := cfg.Embedder.Endpoint
		return embed.NewOllamaEmbedder(endpoint, cfg.Embedder.Model)
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
		endpoint := cfg.Embedder.Endpoint
		return chat.NewOllamaChatModel(endpoint, cfg.Chat.Model), nil
	default:
		return nil, fmt.Errorf("unknown chat provider: %s", cfg.Chat.Provider)
	}
}
