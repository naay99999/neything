package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/spf13/cobra"
)

var (
	flagMigrateVectors bool
	flagNoEmbed        bool
)

var indexCmd = &cobra.Command{
	Use:   "index [path]",
	Short: "Index files in a directory (defaults to asking about the current one)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runIndex,
}

func init() {
	indexCmd.Flags().BoolVar(&flagMigrateVectors, "migrate-vectors", false, "import vectors.bin into hnsw backend without re-embedding")
	indexCmd.Flags().BoolVar(&flagNoEmbed, "no-embed", false, "write chunks and keyword index only; embed later with another ney index run")
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

	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		return err
	}
	defer lock.Release()

	app, err := initAppWithOptions(cfg, flagMigrateVectors, false)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Indexing %s (workspace: %s)...\n", displayPath(rootPath), workspaceName)

	// Phase A: parse + chunk + FTS (never embeds — see internal/index).
	stats, err := ix.Index(cmd.Context(), rootPath, workspaceName)
	if err != nil {
		return err
	}

	// Phase B: drain this workspace's pending embeds synchronously, unless
	// skipped. Embedding failures here don't undo Phase A — the chunks and
	// keyword index are already committed and searchable.
	embedded := 0
	pendingLeft := 0
	var embedErr error
	if app.Embedder != nil && !flagNoEmbed {
		showProgress := colorEnabled && !flagJSON
		worker := &index.EmbedWorker{
			DB:        app.DB,
			Vectors:   app.Vectors,
			Embedder:  app.Embedder,
			Workspace: workspaceName,
			OnProgress: func(done, total int) {
				embedded = done
				pendingLeft = total - done
				if showProgress {
					fmt.Fprintf(os.Stderr, "\r\033[K%s", Dim(fmt.Sprintf("embedding %d/%d", done, total)))
				}
			},
		}
		embedErr = worker.Run(cmd.Context())
		if showProgress {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	} else {
		pendingLeft = stats.ChunksPendingEmbed
	}

	type result struct {
		Workspace          string `json:"workspace"`
		FilesScanned       int    `json:"files_scanned"`
		FilesSkipped       int    `json:"files_skipped"`
		FilesRemoved       int    `json:"files_removed"`
		FilesFailed        int    `json:"files_failed"`
		ChunksCreated      int    `json:"chunks_created"`
		ChunksEmbedded     int    `json:"chunks_embedded"`
		ChunksPendingEmbed int    `json:"chunks_pending_embed"`
		VectorsPruned      int    `json:"vectors_pruned"`
		DurationMs         int64  `json:"duration_ms"`
	}
	r := result{
		Workspace:          workspaceName,
		FilesScanned:       stats.FilesScanned,
		FilesSkipped:       stats.FilesSkipped,
		FilesRemoved:       stats.FilesRemoved,
		FilesFailed:        stats.FilesFailed,
		ChunksCreated:      stats.ChunksCreated,
		ChunksEmbedded:     embedded,
		ChunksPendingEmbed: pendingLeft,
		VectorsPruned:      stats.VectorsPruned,
		DurationMs:         stats.Duration.Milliseconds(),
	}

	if flagJSON {
		PrintJSON(r)
		if embedErr != nil {
			return fmt.Errorf("embedding failed (chunks are saved; run `ney index` again to resume): %w", embedErr)
		}
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
	fmt.Println(Green(fmt.Sprintf("✓ %d chunks written, %d embedded", stats.ChunksCreated, embedded)))
	if stats.VectorsPruned > 0 {
		fmt.Println(Green(fmt.Sprintf("✓ %d vectors pruned", stats.VectorsPruned)))
	}
	if pendingLeft > 0 && embedErr == nil {
		switch {
		case app.Embedder == nil:
			fmt.Println(Dim(fmt.Sprintf("  %d chunks pending embedding — no embedder configured (keyword search works now; run ney init to add one)", pendingLeft)))
		case flagNoEmbed:
			fmt.Println(Dim(fmt.Sprintf("  %d chunks pending embedding — run ney index to embed later", pendingLeft)))
		}
	}
	if embedErr != nil {
		return fmt.Errorf("embedding failed (chunks are saved and keyword-searchable; run `ney index` again to resume): %w", embedErr)
	}
	if stats.FilesFailed > 0 {
		return fmt.Errorf("%d file(s) failed to index (see warnings above)", stats.FilesFailed)
	}
	fmt.Println(Green(fmt.Sprintf("✓ Index ready (%s) in %s", "~/.ney/index.db", stats.Duration.Round(1000000000))))
	return nil
}
