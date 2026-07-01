package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var flagMigrateVectors bool

var indexCmd = &cobra.Command{
	Use:   "index [path]",
	Short: "Index files in a directory (defaults to asking about the current one)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runIndex,
}

func init() {
	indexCmd.Flags().BoolVar(&flagMigrateVectors, "migrate-vectors", false, "import vectors.bin into hnsw backend without re-embedding")
}

func runIndex(cmd *cobra.Command, args []string) error {
	var rawPath string
	switch {
	case len(args) == 1:
		rawPath = args[0]
	case isatty.IsTerminal(os.Stdin.Fd()):
		rawPath = promptIndexPath()
		if rawPath == "" {
			return fmt.Errorf("no path given")
		}
	default:
		return fmt.Errorf("usage: ney index <path>")
	}

	rootPath, err := filepath.Abs(expandTilde(rawPath))
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
	app, err := initAppWithOptions(cfg, flagMigrateVectors, false)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Indexing %s (workspace: %s)...\n", displayPath(rootPath), workspaceName)

	stats, err := ix.Index(cmd.Context(), rootPath, workspaceName)
	if err != nil {
		return err
	}

	type result struct {
		Workspace     string `json:"workspace"`
		FilesScanned  int    `json:"files_scanned"`
		FilesSkipped  int    `json:"files_skipped"`
		FilesRemoved  int    `json:"files_removed"`
		FilesFailed   int    `json:"files_failed"`
		ChunksCreated int    `json:"chunks_created"`
		VectorsPruned int    `json:"vectors_pruned"`
		DurationMs    int64  `json:"duration_ms"`
	}
	r := result{
		Workspace:     workspaceName,
		FilesScanned:  stats.FilesScanned,
		FilesSkipped:  stats.FilesSkipped,
		FilesRemoved:  stats.FilesRemoved,
		FilesFailed:   stats.FilesFailed,
		ChunksCreated: stats.ChunksCreated,
		VectorsPruned: stats.VectorsPruned,
		DurationMs:    stats.Duration.Milliseconds(),
	}

	if flagJSON {
		PrintJSON(r)
		if stats.FilesFailed > 0 {
			return fmt.Errorf("%d file(s) failed to index", stats.FilesFailed)
		}
		return nil
	}

	fmt.Println(Green(fmt.Sprintf("✓ %d files scanned (workspace: %s)", stats.FilesScanned, workspaceName)))
	fmt.Println(Green(fmt.Sprintf("✓ %d files skipped (unchanged)", stats.FilesSkipped)))
	if stats.FilesRemoved > 0 {
		fmt.Println(Green(fmt.Sprintf("✓ %d files removed from index", stats.FilesRemoved)))
	}
	fmt.Println(Green(fmt.Sprintf("✓ %d chunks embedded", stats.ChunksCreated)))
	if stats.VectorsPruned > 0 {
		fmt.Println(Green(fmt.Sprintf("✓ %d vectors pruned", stats.VectorsPruned)))
	}
	if stats.FilesFailed > 0 {
		return fmt.Errorf("%d file(s) failed to index (see warnings above)", stats.FilesFailed)
	}
	fmt.Println(Green(fmt.Sprintf("✓ Index ready (%s) in %s", "~/.ney/index.db", stats.Duration.Round(1000000000))))
	return nil
}
