package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ney",
	Short: "Local-first AI knowledge engine",
	Long:  "Ney — give your AI agent search + read access to your local documents over MCP. Local-first: your files never leave your machine.",
}

var (
	flagWorkspace string
	flagTopK      int
	flagJSON      bool
	flagPath      string
	flagAll       bool
)

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	rootCmd.PersistentFlags().StringVar(&flagWorkspace, "workspace", "", "workspace name")
	rootCmd.PersistentFlags().IntVar(&flagTopK, "top-k", 8, "number of chunks to retrieve")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON")
	rootCmd.PersistentFlags().StringVar(&flagPath, "path", "", "limit scope to path")
	rootCmd.PersistentFlags().BoolVar(&flagAll, "all", false, "search across all workspaces, ignoring the current folder's scope")

	rootCmd.AddCommand(
		initCmd,
		indexCmd,
		watchCmd,
		searchCmd,
		statusCmd,
		configCmd,
		doctorCmd,
		versionCmd,
		resetCmd,
		mcpCmd,
	)
}

// loadConfig wraps config.Load and owns the first-run hint. config.Load is
// silent by construction so `ney mcp` can never emit it; the hint is printed
// here, to stderr, only for an interactive human.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	if cfg.CreatedDefault && !flagJSON && isatty.IsTerminal(os.Stderr.Fd()) {
		fmt.Fprintf(os.Stderr, "Created default config at %s\n\n", config.ConfigPath())
	}
	return cfg, nil
}

func Execute() error {
	return rootCmd.Execute()
}

func main() {
	// Bare `ney` on a machine that has never completed setup offers the
	// wizard (interactive terminals only); everywhere else it falls through
	// to cobra's help.
	if len(os.Args) == 1 && !setupCompleted() && isatty.IsTerminal(os.Stdin.Fd()) {
		ans := strings.ToLower(promptLine("Ney isn't set up on this machine yet — start setup now? [Y/n] "))
		if ans == "" || strings.HasPrefix(ans, "y") {
			if err := runSetupWizard(context.Background()); err != nil {
				printCLIError(err)
				os.Exit(1)
			}
			return
		}
	}
	if err := Execute(); err != nil {
		printCLIError(err)
		os.Exit(1)
	}
}
