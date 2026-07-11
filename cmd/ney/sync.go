package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/lockfile"
)

// syncEmbedMaxChunks bounds how many chunks a pre-search sync will embed
// before handing the rest off to an explicit `ney index` run — a sync runs
// in the critical path of `ney search`/`ask` and must stay snappy even when
// a big backlog is pending.
const syncEmbedMaxChunks = 256

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

	// Phase B, bounded: embed at most syncEmbedMaxChunks of this
	// workspace's pending chunks and tell the user how to finish the rest.
	// Like everything else here, embed problems are reported, never fatal —
	// keyword search already covers the chunks Phase A just wrote.
	if app.Embedder == nil {
		return
	}
	worker := &index.EmbedWorker{
		DB:        app.DB,
		Vectors:   app.Vectors,
		Embedder:  app.Embedder,
		Workspace: ws.Name,
		MaxChunks: syncEmbedMaxChunks,
	}
	sp = startSpinner("embedding")
	err = worker.Run(ctx)
	sp.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: embedding sync failed: %v\n", err)
		return
	}
	// The bounded run may have left backlog beyond its cap; recount from
	// the source of truth rather than trusting this run's progress math.
	if remaining := countPendingEmbeds(app, ws.ID); remaining > 0 {
		fmt.Fprintf(os.Stderr, "%d chunks awaiting embedding — run `ney index`\n", remaining)
	}
}

// countPendingEmbeds returns how many of a workspace's chunks still lack a
// vector. Cheap: one fully-drained query plus an in-memory set diff (vector
// IDs are chunk row IDs as decimal strings).
func countPendingEmbeds(app *AppState, workspaceID int64) int {
	chunkIDs, err := app.DB.GetChunkIDsByWorkspace(workspaceID)
	if err != nil {
		return 0
	}
	vecIDs := make(map[string]bool)
	for _, id := range app.Vectors.IDs() {
		vecIDs[id] = true
	}
	pending := 0
	for _, id := range chunkIDs {
		if !vecIDs[strconv.FormatInt(id, 10)] {
			pending++
		}
	}
	return pending
}
