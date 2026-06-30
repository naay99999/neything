package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var flagMigrateVectors bool

var indexCmd = &cobra.Command{
	Use:   "index <path>",
	Short: "Index files in a directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runIndex,
}

func init() {
	indexCmd.Flags().BoolVar(&flagMigrateVectors, "migrate-vectors", false, "import vectors.bin into hnsw backend without re-embedding")
}

func runIndex(cmd *cobra.Command, args []string) error {
	rootPath, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	if _, err := os.Stat(rootPath); err != nil {
		return fmt.Errorf("path not found: %s", rootPath)
	}

	workspaceName := flagWorkspace
	if workspaceName == "" {
		workspaceName = filepath.Base(rootPath)
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	applyProviderOverride(cfg, true)
	app, err := initAppWithOptions(cfg, flagMigrateVectors)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Indexing %s (workspace: %s)...\n", rootPath, workspaceName)

	stats, err := ix.Index(cmd.Context(), rootPath, workspaceName)
	if err != nil {
		return err
	}

	type result struct {
		Workspace     string `json:"workspace"`
		FilesScanned  int    `json:"files_scanned"`
		FilesSkipped  int    `json:"files_skipped"`
		FilesRemoved  int    `json:"files_removed"`
		ChunksCreated int    `json:"chunks_created"`
		VectorsPruned int    `json:"vectors_pruned"`
		DurationMs    int64  `json:"duration_ms"`
	}
	r := result{
		Workspace:     workspaceName,
		FilesScanned:  stats.FilesScanned,
		FilesSkipped:  stats.FilesSkipped,
		FilesRemoved:  stats.FilesRemoved,
		ChunksCreated: stats.ChunksCreated,
		VectorsPruned: stats.VectorsPruned,
		DurationMs:    stats.Duration.Milliseconds(),
	}

	if flagJSON {
		PrintJSON(r)
		return nil
	}

	fmt.Printf("✓ %d files scanned (workspace: %s)\n", stats.FilesScanned, workspaceName)
	fmt.Printf("✓ %d files skipped (unchanged)\n", stats.FilesSkipped)
	if stats.FilesRemoved > 0 {
		fmt.Printf("✓ %d files removed from index\n", stats.FilesRemoved)
	}
	fmt.Printf("✓ %d chunks embedded\n", stats.ChunksCreated)
	if stats.VectorsPruned > 0 {
		fmt.Printf("✓ %d vectors pruned\n", stats.VectorsPruned)
	}
	fmt.Printf("✓ Index ready (%s) in %s\n", "~/.ney/index.db", stats.Duration.Round(1000000000))
	return nil
}
