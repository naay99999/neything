package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/lockfile"
)

// syncWorkspaceIfKnown silently re-indexes the workspace bound to the
// current directory before a search, so edits made outside `ney` are picked
// up without the user having to remember to run `index` again. It only acts
// when the scope wasn't explicitly overridden (--workspace/--all), reuses
// app's already-open DB/Vectors/Embedder (no concurrent writer), and never
// fails the caller's request — sync problems are reported, not fatal.
//
// It also takes the writer lock (config.NeyDir()) for the duration of the
// sync, since it writes chunks/vectors just like `ney index`. If another
// writer (e.g. a long-lived `ney mcp`) already holds it, the sync is
// skipped rather than blocking or failing the search/ask that triggered it.
func syncWorkspaceIfKnown(ctx context.Context, app *AppState, cfg *config.Config) {
	if flagWorkspace != "" || flagAll {
		return
	}
	ws := cwdWorkspace(app.DB)
	if ws == nil {
		return
	}
	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		if errors.Is(err, lockfile.ErrLocked) {
			fmt.Fprintln(os.Stderr, "index busy — searching existing data")
			return
		}
		fmt.Fprintf(os.Stderr, "warning: sync failed: %v\n", err)
		return
	}
	defer lock.Release()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return
	}
	sp := startSpinner("syncing")
	stats, err := ix.Index(ctx, ws.RootPath, ws.Name)
	sp.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: sync failed: %v\n", err)
		return
	}
	if stats.FilesRemoved > 0 {
		fmt.Fprintf(os.Stderr, "↻ %d removed file(s) pruned from index\n", stats.FilesRemoved)
	}
}
