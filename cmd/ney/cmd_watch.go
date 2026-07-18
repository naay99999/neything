package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/naay99999/neything/internal/watch"
	"github.com/spf13/cobra"
)

var flagWatchDebounce time.Duration

var watchCmd = &cobra.Command{
	Use:   "watch <path>",
	Short: "Watch a directory and re-index on file changes",
	Args:  cobra.ExactArgs(1),
	RunE:  runWatch,
}

func init() {
	watchCmd.Flags().DurationVar(&flagWatchDebounce, "debounce", 2*time.Second, "debounce interval before re-indexing")
}

func runWatch(cmd *cobra.Command, args []string) error {
	rootPath, err := filepath.Abs(expandTilde(args[0]))
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
	applyProviderOverride(cfg)

	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		return err
	}
	defer lock.Release()

	app, err := initAppFromConfig(cfg)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return err
	}

	workspaceID, err := app.DB.UpsertWorkspace(workspaceName, rootPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Watching %s (workspace: %s, debounce: %s)...\n", rootPath, workspaceName, flagWatchDebounce)
	fmt.Fprintf(os.Stderr, "Press Ctrl+C to stop.\n")

	onEvent := func(msg string) {
		fmt.Fprintf(os.Stderr, "  %s\n", msg)
	}

	// Watcher.Run only responds to ctx now (it installs no signal handlers
	// of its own), so we own SIGINT/SIGTERM here and cancel a derived ctx —
	// this is what makes Ctrl+C trigger Run's final-flush-then-prune path
	// and return cleanly.
	watchCtx, cancelWatch := context.WithCancel(cmd.Context())
	defer cancelWatch()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			onEvent("shutting down...")
			cancelWatch()
		case <-watchCtx.Done():
		}
	}()

	// Phase B runs beside the watcher for the whole watch: the watcher only
	// writes chunks+FTS (Phase A), and nudges the worker after every flush
	// batch. The worker gets its own cancel, derived from cmd.Context()
	// (not watchCtx) rather than tied to Watcher.Run's ctx, so Ctrl+C stops
	// the watcher (flush + prune) first, and the worker is stopped
	// explicitly once Watcher.Run returns.
	var worker *index.EmbedWorker
	if app.Embedder != nil {
		workerCtx, stopWorker := context.WithCancel(cmd.Context())
		worker = &index.EmbedWorker{
			DB:       app.DB,
			Vectors:  app.Vectors,
			Embedder: app.Embedder,
		}
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			_ = worker.RunLoop(workerCtx)
		}()
		// Drain any backlog left by earlier runs (e.g. a --no-embed index)
		// before the first filesystem event arrives.
		worker.Notify()
		defer func() {
			stopWorker()
			<-workerDone
		}()
	}

	w := &watch.Watcher{
		Indexer:     ix,
		RootPath:    rootPath,
		WorkspaceID: workspaceID,
		Debounce:    flagWatchDebounce,
		OnEvent:     onEvent,
	}
	if worker != nil {
		w.OnFlush = worker.Notify
	}

	stats, err := w.Run(watchCtx)
	if err != nil {
		return err
	}

	type result struct {
		Workspace     string `json:"workspace"`
		FilesIndexed  int    `json:"files_indexed"`
		FilesRemoved  int    `json:"files_removed"`
		VectorsPruned int    `json:"vectors_pruned"`
		Errors        int    `json:"errors"`
	}
	r := result{
		Workspace:     workspaceName,
		FilesIndexed:  stats.FilesIndexed,
		FilesRemoved:  stats.FilesRemoved,
		VectorsPruned: stats.VectorsPruned,
		Errors:        stats.Errors,
	}

	if flagJSON {
		PrintJSON(r)
		return nil
	}

	fmt.Printf("✓ Watch stopped (workspace: %s)\n", workspaceName)
	fmt.Printf("✓ %d files indexed\n", stats.FilesIndexed)
	if stats.FilesRemoved > 0 {
		fmt.Printf("✓ %d files removed\n", stats.FilesRemoved)
	}
	if stats.VectorsPruned > 0 {
		fmt.Printf("✓ %d vectors pruned\n", stats.VectorsPruned)
	}
	if stats.Errors > 0 {
		fmt.Printf("⚠ %d errors (see stderr)\n", stats.Errors)
	}
	return nil
}
