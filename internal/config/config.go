package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Context   ContextConfig  `mapstructure:"context"`
	Handoff   HandoffConfig  `mapstructure:"handoff"`
	Projects  ProjectsConfig `mapstructure:"projects"`
	Telemetry bool           `mapstructure:"telemetry"`
	// Sources lists the agent transcript sources handoff reads. Defaults to
	// ["claude-code", "codex"].
	Sources []string `mapstructure:"sources"`

	// CreatedDefault reports whether this Load call created ~/.ney/config.yaml
	// from the template. It is not a config key: Load itself is silent (it is
	// reachable from `ney mcp`, whose stdout is the MCP protocol), so the CLI
	// reads this to print a one-time hint. json:"-" keeps it out of
	// `ney config show --json`, which marshals Config directly.
	CreatedDefault bool `mapstructure:"-" json:"-"`
}

// ContextConfig controls the "layered context" get_context surface
// (internal/context): where to look for git repos on disk, and how wide the
// "active" window is when deciding which projects to surface.
type ContextConfig struct {
	// DevRoots is where ScanRepos looks for git repositories. Defaults to
	// ["~/workspace"] if that directory exists at load time, else empty (no
	// live repo scan; get_context then lists no projects).
	// Leading "~" is expanded against the user's home directory on load.
	DevRoots []string `mapstructure:"dev_roots"`
	// ActiveDays is the recency window (in days) a project's last commit
	// must fall within to count as "active" in get_context. Defaults to 14.
	ActiveDays int `mapstructure:"active_days"`
}

// HandoffConfig controls session handoff briefs.
type HandoffConfig struct {
	// Auto enables the SessionStart hook injection. Defaults to true.
	Auto bool `mapstructure:"auto"`
	// MaxAgeDays is how far back sessions are considered. Defaults to 14.
	MaxAgeDays int `mapstructure:"max_age_days"`
	// DisabledRepos are repo roots where the hook stays silent. A leading "~"
	// is expanded on load.
	DisabledRepos []string `mapstructure:"disabled_repos"`
}

// ProjectsConfig controls project memory: the projects section injected at
// session start and when a project's card counts as stale.
type ProjectsConfig struct {
	// Inject adds the projects section at SessionStart. Defaults to true.
	Inject bool `mapstructure:"inject"`
	// StaleCommits: a card is stale once this many commits landed after it
	// was written. Defaults to 30.
	StaleCommits int `mapstructure:"stale_commits"`
	// StaleDays: a card is stale once it is this many days old. Defaults to 60.
	StaleDays int `mapstructure:"stale_days"`
	// NamesInBrief caps the project names listed at session start. Defaults to 20.
	NamesInBrief int `mapstructure:"names_in_brief"`
}

// knownSources are the valid names for Config.Sources.
var knownSources = map[string]bool{"claude-code": true, "codex": true}

const defaultConfig = `# Ney configuration (~/.ney/config.yaml)
#
# ney is a local-first personal context server for AI clients (MCP).
# 'ney mcp' gives Claude Code / Codex session handoff and project memory.
# Everything runs on your machine — no API key, no model server, no network.
#
# Run 'ney init' for guided setup. See README.md.

# where to look for git repos (feeds list_projects),
# and how recent a project's last commit must be to count as "active"
context:
  # dev_roots defaults to ["~/workspace"] if that directory exists (checked
  # fresh on every load), else no repo scan runs — uncomment to override:
  # dev_roots: ["~/workspace", "~/code"]
  active_days: 14

# project memory — defaults shown; uncomment to override
# projects:
#   inject: true          # list your projects at session start
#   stale_commits: 30     # a card is stale after this many new commits
#   stale_days: 60        # ... or after this many days
#   names_in_brief: 20    # max project names listed at session start

# privacy — off by default
telemetry: false
`

func NeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ney")
}

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

	if !v.IsSet("handoff.auto") {
		cfg.Handoff.Auto = true
	}
	if cfg.Handoff.MaxAgeDays == 0 {
		cfg.Handoff.MaxAgeDays = 14
	}
	for i, r := range cfg.Handoff.DisabledRepos {
		cfg.Handoff.DisabledRepos[i] = expandTilde(r)
	}
	if !v.IsSet("projects.inject") {
		cfg.Projects.Inject = true
	}
	if !v.IsSet("projects.stale_commits") {
		cfg.Projects.StaleCommits = 30
	}
	if !v.IsSet("projects.stale_days") {
		cfg.Projects.StaleDays = 60
	}
	if !v.IsSet("projects.names_in_brief") {
		cfg.Projects.NamesInBrief = 20
	}
	if !v.IsSet("sources") {
		cfg.Sources = []string{"claude-code", "codex"}
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	cfg.CreatedDefault = createdDefault
	return &cfg, nil
}

// Validate checks the settings that have real consequences if wrong. Keys
// from older versions (retrieval, index, chunking, ...) are ignored by Load
// and never validated.
func Validate(cfg *Config) error {
	// 0 is accepted here (treated as "unset" by structs built directly in
	// tests without setting Context.ActiveDays); Load() always normalizes it
	// to a concrete positive value via its default-application step.
	if cfg.Context.ActiveDays < 0 {
		return fmt.Errorf("context.active_days must be greater than 0, got %d", cfg.Context.ActiveDays)
	}
	if cfg.Handoff.MaxAgeDays < 0 {
		return fmt.Errorf("handoff.max_age_days must not be negative, got %d", cfg.Handoff.MaxAgeDays)
	}
	for key, n := range map[string]int{
		"projects.stale_commits":  cfg.Projects.StaleCommits,
		"projects.stale_days":     cfg.Projects.StaleDays,
		"projects.names_in_brief": cfg.Projects.NamesInBrief,
	} {
		if n < 0 {
			return fmt.Errorf("%s must not be negative, got %d", key, n)
		}
	}
	for _, name := range cfg.Sources {
		if !knownSources[name] {
			return fmt.Errorf("unknown source %q in sources (valid: claude-code, codex)", name)
		}
	}
	return nil
}

// defaultDevRoots returns ["~/workspace"] (expanded) if that directory
// exists at load time, else an empty slice — an install with no dev_roots
// configured and no ~/workspace simply gets no live repo scan; get_context
// then lists no projects.
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
