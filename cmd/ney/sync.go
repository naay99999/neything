package main

import (
	"context"
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/config"
)

// syncWorkspaceIfKnown silently re-indexes the workspace bound to the
// current directory before a search, so edits made outside `ney` are picked
// up without the user having to remember to run `index` again. It only acts
// when the scope wasn't explicitly overridden (--workspace/--all), reuses
// app's already-open DB/Vectors/Embedder (no concurrent writer), and never
// fails the caller's request — sync problems are reported, not fatal.
func syncWorkspaceIfKnown(ctx context.Context, app *AppState, cfg *config.Config) {
	if flagWorkspace != "" || flagAll {
		return
	}
	ws := cwdWorkspace(app.DB)
	if ws == nil {
		return
	}
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
