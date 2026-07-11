package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/watch"
	"github.com/spf13/cobra"
)

var flagMCPRoots []string

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run ney as an MCP server over stdio (search_documents, read_document, list_workspaces, index_status)",
	Long: "Run ney as an MCP server over stdio, for AI clients like Claude Code/Desktop/Cursor.\n" +
		"Serves query tools immediately while indexing/embedding continue in the background.\n" +
		"stdout is reserved for the MCP protocol — all diagnostics go to stderr.",
	RunE: runMCP,
}

func init() {
	mcpCmd.Flags().StringArrayVar(&flagMCPRoots, "root", nil,
		"workspace root to index and serve (repeatable; defaults to workspaces already in the index)")
}

// mcpRoot is a resolved (absolute, symlink-evaluated) workspace root the
// server indexes, watches, and allows read_document to read under.
type mcpRoot struct {
	Name string
	Path string
}

// runMCP wires one long-lived server process: acquire the writer lock, open
// the DB/Vectors/Embedder once for the process's lifetime, register the 4
// MCP tools, and start serving over stdio immediately — Phase A indexing,
// background embedding, and filesystem watching all happen in goroutines
// alongside it, never blocking the first tool call.
//
// STDOUT HYGIENE: stdout is reserved for the MCP protocol end to end. This
// function and everything it calls (loadConfig, initAppWithOptions,
// newIndexer, the watcher, the embed worker) only ever write diagnostics to
// os.Stderr — never fmt.Println/PrintJSON/the banner/spinner helpers, all of
// which write to os.Stdout. Because `ney mcp` always has an arg (main.go only
// calls runREPL for a bare `ney` with zero args), the interactive
// banner/onboarding path in banner.go is never reached here either.
func runMCP(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		// lockfile.ErrLocked's message ("another ney process (pid X, ney mcp)
		// is writing — stop it first") is already friendly and specific;
		// main() routes returned errors to printCLIError, which writes to
		// stderr and exits non-zero.
		return err
	}
	defer lock.Release()

	app, err := initAppWithOptions(cfg, false, false)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	roots, err := resolveMCPRoots(app.DB, flagMCPRoots)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "ney mcp: no workspaces to serve yet — pass --root <path>, or run `ney index` first")
	}

	ix, err := newIndexer(app, cfg)
	if err != nil {
		return err
	}

	state := newServerState(rootNames(roots))

	var worker *index.EmbedWorker
	if app.Embedder != nil {
		worker = &index.EmbedWorker{DB: app.DB, Vectors: app.Vectors, Embedder: app.Embedder}
	}

	server := newMCPServer(app, cfg, state, roots, worker)

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// indexMu serializes every Indexer invocation across Phase A and every
	// root's watcher — Watcher.Serialize routes through it too, matching the
	// single shared mutex the design calls for (§7.4).
	var indexMu sync.Mutex
	var wg sync.WaitGroup

	// (a) Phase A of each root, sequentially, so a big corpus doesn't starve
	// the others — each root's tool calls are already served (FTS/tier-0-ish
	// partial results) while later roots are still being scanned.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, r := range roots {
			if ctx.Err() != nil {
				return
			}
			state.setPhaseA(r.Name, true)
			indexMu.Lock()
			_, ierr := ix.Index(ctx, r.Path, r.Name)
			indexMu.Unlock()
			state.setPhaseA(r.Name, false)
			if ierr != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", r.Name, ierr)
			}
			if worker != nil {
				worker.Notify()
			}
		}
	}()

	// (b) EmbedWorker.RunLoop — global scope (every workspace), notified
	// after each Phase A root completes and after every watcher flush.
	if worker != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = worker.RunLoop(ctx)
		}()
	}

	// (c) one watcher per root. Prune is centralized on the first watcher
	// only (DisablePrune on the rest) per §7.4 — an external owner (this
	// server) is expected to centralize pruning rather than each Watcher
	// running its own ticker.
	for i, r := range roots {
		wsID, werr := app.DB.UpsertWorkspace(r.Name, r.Path)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "warning: watch %s: %v\n", r.Name, werr)
			continue
		}
		w := &watch.Watcher{
			Indexer:      ix,
			RootPath:     r.Path,
			WorkspaceID:  wsID,
			DisablePrune: i != 0,
			Serialize: func(run func()) {
				indexMu.Lock()
				defer indexMu.Unlock()
				run()
			},
		}
		if worker != nil {
			w.OnFlush = worker.Notify
		}
		wg.Add(1)
		go func(w *watch.Watcher) {
			defer wg.Done()
			if _, werr := w.Run(ctx); werr != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "warning: watcher %s: %v\n", w.RootPath, werr)
			}
		}(w)
	}
	if len(roots) > 0 {
		state.setWatching(true)
	}

	fmt.Fprintf(os.Stderr, "ney mcp: serving %d workspace(s) over stdio (Ctrl+C or close stdin to stop)\n", len(roots))

	runErr := server.Run(ctx, &mcp.StdioTransport{})

	// Shutdown: stop background goroutines, then flush + release the lock.
	stop()
	wg.Wait()
	if ferr := app.Vectors.Flush(); ferr != nil {
		fmt.Fprintf(os.Stderr, "warning: flush vectors: %v\n", ferr)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}

// resolveMCPRoots turns --root flags (or, if none were given, every
// workspace already in the DB) into a resolved root list. A --root whose
// basename collides with an existing workspace bound to a *different*
// root_path is a hard error — UpsertWorkspace's ON CONFLICT(name) DO UPDATE
// would otherwise silently re-point that workspace's root_path (CLAUDE.md;
// design §7.2), which would orphan its already-indexed documents.
func resolveMCPRoots(db *store.DB, rawRoots []string) ([]mcpRoot, error) {
	if len(rawRoots) == 0 {
		wss, err := db.ListWorkspaces()
		if err != nil {
			return nil, err
		}
		roots := make([]mcpRoot, 0, len(wss))
		for _, ws := range wss {
			roots = append(roots, mcpRoot{Name: ws.Name, Path: resolveRootBestEffort(ws.RootPath)})
		}
		return roots, nil
	}

	roots := make([]mcpRoot, 0, len(rawRoots))
	for _, raw := range rawRoots {
		abs, err := filepath.Abs(expandTilde(raw))
		if err != nil {
			return nil, fmt.Errorf("--root %q: %w", raw, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("--root %q: path not found: %s", raw, abs)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("--root %q: %w", raw, err)
		}
		resolved = filepath.Clean(resolved)

		name := filepath.Base(resolved)
		existing, err := db.GetWorkspaceByName(name)
		if err != nil {
			return nil, err
		}
		if existing != nil && resolveRootBestEffort(existing.RootPath) != resolved {
			return nil, fmt.Errorf(
				"workspace %q is already bound to %s — pass a different --root path, or index this one under its own name first with `ney index %s --workspace <name>`",
				name, existing.RootPath, raw,
			)
		}
		if _, err := db.UpsertWorkspace(name, resolved); err != nil {
			return nil, err
		}
		roots = append(roots, mcpRoot{Name: name, Path: resolved})
	}
	return roots, nil
}

// resolveRootBestEffort symlink-resolves and cleans path, falling back to a
// plain Clean if the path no longer exists (e.g. a workspace whose directory
// was since removed) — read_document's path guard and the background
// indexers still need a canonical string to compare/walk even then.
func resolveRootBestEffort(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func rootNames(roots []mcpRoot) []string {
	names := make([]string, len(roots))
	for i, r := range roots {
		names[i] = r.Name
	}
	return names
}

// serverState tracks the parts of `ney mcp`'s background work that
// index_status (and search_documents' partial-results hint) need to report:
// which roots' Phase A scan is still running, and whether any watcher is
// active. Guarded by a mutex since it's written from background goroutines
// and read from concurrent tool-call handlers.
type serverState struct {
	mu       sync.Mutex
	phaseA   map[string]bool
	watching bool
}

func newServerState(names []string) *serverState {
	s := &serverState{phaseA: make(map[string]bool, len(names))}
	for _, n := range names {
		s.phaseA[n] = false
	}
	return s
}

func (s *serverState) setPhaseA(name string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phaseA[name] = running
}

// anyPhaseARunning reports whether any root's Phase A scan is still in
// flight — used to tell an MCP client its search results may be partial.
func (s *serverState) anyPhaseARunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, running := range s.phaseA {
		if running {
			return true
		}
	}
	return false
}

// runningRoots returns the (sorted) names of roots whose Phase A scan is
// currently in flight.
func (s *serverState) runningRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for name, running := range s.phaseA {
		if running {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *serverState) setWatching(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watching = v
}

func (s *serverState) isWatching() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watching
}
