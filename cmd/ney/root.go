package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ney",
	Short: "Local-first AI knowledge engine",
	Long:  "Ney — index your files, search by meaning, ask questions with source citations.",
}

var (
	flagWorkspace string
	flagTopK      int
	flagProvider  string
	flagJSON      bool
	flagPath      string
	flagAll       bool
)

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	rootCmd.PersistentFlags().StringVar(&flagWorkspace, "workspace", "", "workspace name")
	rootCmd.PersistentFlags().IntVar(&flagTopK, "top-k", 8, "number of chunks to retrieve")
	rootCmd.PersistentFlags().StringVar(&flagProvider, "provider", "", "override provider (embedder for index/search/watch, chat for ask)")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON")
	rootCmd.PersistentFlags().StringVar(&flagPath, "path", "", "limit scope to path")
	rootCmd.PersistentFlags().BoolVar(&flagAll, "all", false, "search across all workspaces, ignoring the current folder's scope")

	rootCmd.AddCommand(
		initCmd,
		indexCmd,
		watchCmd,
		searchCmd,
		askCmd,
		statusCmd,
		configCmd,
		doctorCmd,
		modelsCmd,
		versionCmd,
		resetCmd,
		mcpCmd,
	)
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	return cfg, nil
}

func Execute() error {
	return rootCmd.Execute()
}

func main() {
	var err error
	if len(os.Args) == 1 {
		err = runREPL()
	} else {
		err = Execute()
	}
	if err != nil {
		printCLIError(err)
		os.Exit(1)
	}
}
