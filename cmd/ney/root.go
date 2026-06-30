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
)

func init() {
	rootCmd.PersistentFlags().StringVar(&flagWorkspace, "workspace", "", "workspace name")
	rootCmd.PersistentFlags().IntVar(&flagTopK, "top-k", 8, "number of chunks to retrieve")
	rootCmd.PersistentFlags().StringVar(&flagProvider, "provider", "", "override provider")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "output JSON")
	rootCmd.PersistentFlags().StringVar(&flagPath, "path", "", "limit scope to path")

	rootCmd.AddCommand(
		indexCmd,
		searchCmd,
		askCmd,
		statusCmd,
		configCmd,
		doctorCmd,
		modelsCmd,
		versionCmd,
		resetCmd,
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
	if err := Execute(); err != nil {
		os.Exit(1)
	}
}
