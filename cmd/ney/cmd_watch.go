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

	w := &watch.Watcher{
		Indexer:     ix,
		RootPath:    rootPath,
		WorkspaceID: workspaceID,
		Debounce:    flagWatchDebounce,
		OnEvent:     onEvent,
	}

	stats, err := w.Run(watchCtx)
	if err != nil {
		return err
	}

	type result struct {
		Workspace    string `json:"workspace"`
		FilesIndexed int    `json:"files_indexed"`
		FilesRemoved int    `json:"files_removed"`
		Errors       int    `json:"errors"`
	}
	r := result{
		Workspace:    workspaceName,
		FilesIndexed: stats.FilesIndexed,
		FilesRemoved: stats.FilesRemoved,
		Errors:       stats.Errors,
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
	if stats.Errors > 0 {
		fmt.Printf("⚠ %d errors (see stderr)\n", stats.Errors)
	}
	return nil
}
