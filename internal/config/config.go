package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/naay99999/neything/internal/pathfilter"
	"github.com/spf13/viper"
)

type Config struct {
	Retrieval RetrievalConfig `mapstructure:"retrieval"`
	Index     IndexConfig     `mapstructure:"index"`
	Chunking  ChunkingConfig  `mapstructure:"chunking"`
	Context   ContextConfig   `mapstructure:"context"`
	Telemetry bool            `mapstructure:"telemetry"`

	// CreatedDefault reports whether this Load call created ~/.ney/config.yaml
	// from the template. It is not a config key: Load itself is silent (it is
	// reachable from `ney mcp`, whose stdout is the MCP protocol), so the CLI
	// reads this to print a one-time hint. json:"-" keeps it out of
	// `ney config show --json`, which marshals Config directly.
	CreatedDefault bool `mapstructure:"-" json:"-"`
}

// IndexConfig controls what the indexer (and live scan / read_document)
// will touch. Exclude patterns are globs matched case-insensitively against
// file and directory basenames, on top of the built-in always-on deny list
// (dotfiles + common secret-file names — see internal/pathfilter).
type IndexConfig struct {
	Exclude []string `mapstructure:"exclude"`
}

type RetrievalConfig struct {
	TopK int `mapstructure:"top_k"`
}

type ChunkingConfig struct {
	Strategy      string            `mapstructure:"strategy"`
	TargetChars   int               `mapstructure:"target_chars"`
	OverlapChars  int               `mapstructure:"overlap_chars"`
	TargetTokens  int               `mapstructure:"target_tokens"`
	OverlapTokens int               `mapstructure:"overlap_tokens"`
	ByFormat      map[string]string `mapstructure:"by_format"`
}

// ContextConfig controls the "layered context" get_context/list_projects
// surface (internal/context): where to look for git repos on disk, and how
// wide the "active" window is when deciding which projects to surface.
type ContextConfig struct {
	// DevRoots is where ScanRepos looks for git repositories. Defaults to
	// ["~/workspace"] if that directory exists at load time, else empty (no
	// live repo scan; get_context still works off indexed workspaces alone).
	// Leading "~" is expanded against the user's home directory on load.
	DevRoots []string `mapstructure:"dev_roots"`
	// ActiveDays is the recency window (in days) a project's last commit
	// must fall within to count as "active" in get_context. Defaults to 14.
	ActiveDays int `mapstructure:"active_days"`
}

const defaultConfig = `# Ney configuration (~/.ney/config.yaml)
#
# ney is a local-first personal context server for AI clients (MCP).
# Search is keyword-only (SQLite FTS5) and runs entirely on your machine —
# no API key, no model server, no embeddings. 'ney mcp' gives Claude
# Code/Desktop/Cursor/Codex get_context, list_projects, search_documents,
# search_folder, read_document, remember, update_profile, index_folder and
# index_status. Indexes markdown (.md/.markdown, + Obsidian/Notion) and
# plain .txt files.
#
# Run 'ney init' for guided setup. See README.md.

# retrieval settings
retrieval:
  top_k: 8

# indexing — extra exclude patterns (globs matched case-insensitively
# against file and directory names). These add to the built-in always-on
# excludes: dotfiles/dot-directories and common secret-file names
# (*secret*, *credential*, *password*, *.key, *.pem, id_rsa*, ...).
index:
  exclude: []
  # exclude: ["*.bak", "drafts-*", "node_modules"]

# chunking settings
chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer
  target_chars: 1200
  overlap_chars: 150
  target_tokens: 300        # tokenizer strategy only (~4 chars/token)
  overlap_tokens: 50
  # by_format used when strategy: auto
  # by_format:
  #   md: markdown
  #   txt: paragraph

# layered context (get_context / list_projects): where to look for git repos,
# and how recent a project's last commit must be to count as "active"
context:
  # dev_roots defaults to ["~/workspace"] if that directory exists (checked
  # fresh on every load), else no repo scan runs — uncomment to override:
  # dev_roots: ["~/workspace", "~/code"]
  active_days: 14

# privacy — off by default
telemetry: false
`

func NeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ney")
}

func DBPath() string     { return filepath.Join(NeyDir(), "index.db") }
func ConfigPath() string { return filepath.Join(NeyDir(), "config.yaml") }

// ensureConfigFile creates ~/.ney (0700) and ~/.ney/config.yaml (0600) from
// the embedded template when the config file does not exist yet. It reports
// whether it created the file. Shared by Load and SetDevRoots so there is one
// definition of "a config file exists".
func ensureConfigFile() (created bool, err error) {
	cfgPath := ConfigPath()
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		return false, nil
	}
	if err := os.MkdirAll(NeyDir(), 0700); err != nil {
		return false, fmt.Errorf("create ~/.ney: %w", err)
	}
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0600); err != nil {
		return false, fmt.Errorf("write default config: %w", err)
	}
	return true, nil
}

// Load reads ~/.ney/config.yaml, creating it from the template on first run.
//
// Load never writes to stdout or stderr: it is reachable from `ney mcp`,
// whose stdout is reserved for the MCP protocol. First-run hints belong in
// cmd/ney's loadConfig, gated on Config.CreatedDefault and a TTY.
func Load() (*Config, error) {
	cfgPath := ConfigPath()
	createdDefault, err := ensureConfigFile()
	if err != nil {
		return nil, err
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
	if cfg.Context.ActiveDays == 0 {
		cfg.Context.ActiveDays = 14
	}
	if !v.IsSet("context.dev_roots") {
		cfg.Context.DevRoots = defaultDevRoots()
	} else {
		for i, r := range cfg.Context.DevRoots {
			cfg.Context.DevRoots[i] = expandTilde(r)
		}
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	cfg.CreatedDefault = createdDefault
	return &cfg, nil
}

// Validate checks the settings that have real consequences if wrong. The
// index.exclude check matters most: a malformed glob makes the caller's
// pathfilter fall back to built-ins only, which would silently stop honouring
// the user's exclude patterns.
func Validate(cfg *Config) error {
	if _, err := pathfilter.New(cfg.Index.Exclude); err != nil {
		return fmt.Errorf("index.exclude: %w", err)
	}
	validChunk := map[string]bool{
		"auto": true, "character": true, "paragraph": true, "markdown": true, "sentence": true, "tokenizer": true,
	}
	if !validChunk[cfg.Chunking.Strategy] {
		return fmt.Errorf("unknown chunking strategy %q (valid: auto, character, paragraph, markdown, sentence, tokenizer)", cfg.Chunking.Strategy)
	}
	if cfg.Chunking.Strategy == "auto" {
		validPerFormat := map[string]bool{
			"character": true, "paragraph": true, "markdown": true, "sentence": true, "tokenizer": true,
		}
		for docType, strat := range cfg.Chunking.ByFormat {
			if !validPerFormat[strat] {
				return fmt.Errorf("unknown chunking.by_format.%s strategy %q", docType, strat)
			}
		}
	}
	// An overlap at or above the target leaves no forward progress between
	// windows: every chunk would start where the previous one did, and the
	// chunker's "never go backwards" guard degrades to one character per
	// chunk. Rejecting it here turns a config that silently produces a
	// useless, enormous index into a startup error. As above, 0 means "unset"
	// for structs built directly in tests — Load() fills both in first.
	if cfg.Chunking.TargetChars > 0 && cfg.Chunking.OverlapChars >= cfg.Chunking.TargetChars {
		return fmt.Errorf("chunking.overlap_chars (%d) must be less than chunking.target_chars (%d)",
			cfg.Chunking.OverlapChars, cfg.Chunking.TargetChars)
	}
	if cfg.Chunking.TargetTokens > 0 && cfg.Chunking.OverlapTokens >= cfg.Chunking.TargetTokens {
		return fmt.Errorf("chunking.overlap_tokens (%d) must be less than chunking.target_tokens (%d)",
			cfg.Chunking.OverlapTokens, cfg.Chunking.TargetTokens)
	}
	// 0 is accepted here (treated as "unset" by structs built directly in
	// tests without setting Context.ActiveDays); Load() always normalizes it
	// to a concrete positive value via its default-application step.
	if cfg.Context.ActiveDays < 0 {
		return fmt.Errorf("context.active_days must be greater than 0, got %d", cfg.Context.ActiveDays)
	}
	return nil
}

// defaultDevRoots returns ["~/workspace"] (expanded) if that directory
// exists at load time, else an empty slice — an install with no dev_roots
// configured and no ~/workspace simply gets no live repo scan; get_context
// still works off indexed workspaces alone.
func defaultDevRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return []string{}
	}
	ws := filepath.Join(home, "workspace")
	if fi, err := os.Stat(ws); err == nil && fi.IsDir() {
		return []string{ws}
	}
	return []string{}
}

// expandTilde resolves a leading "~" or "~/..." in p to the user's home
// directory. Paths without a leading ~ are returned unchanged.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	return p
}
