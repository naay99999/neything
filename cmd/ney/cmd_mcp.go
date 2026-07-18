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
// os.Stderr — never fmt.Println/PrintJSON/the spinner helpers, all of which
// write to os.Stdout.
func runMCP(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	lock, readOnly, err := acquireWriterLock(config.NeyDir())
	if err != nil {
		return err
	}
	defer lock.Release() // nil-safe in read-only mode

	app, err := initAppWithOptions(cfg, false)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	roots, err := resolveMCPRoots(app.DB, flagMCPRoots, readOnly)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "ney mcp: no workspaces to serve yet — pass --root <path>, or run `ney index` first")
	}

	state := newServerState(rootNames(roots), readOnly)
	rs := newRootSet(roots)

	var worker *index.EmbedWorker
	if app.Embedder != nil && !readOnly {
		worker = &index.EmbedWorker{DB: app.DB, Vectors: app.Vectors, Embedder: app.Embedder}
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// indexMu serializes every Indexer invocation across Phase A, every
	// root's watcher, and mid-session index_folder calls — Watcher.Serialize
	// routes through it too, matching the single shared mutex the design
	// calls for (§7.4).
	var indexMu sync.Mutex
	var wg sync.WaitGroup
	serialize := func(run func()) {
		indexMu.Lock()
		defer indexMu.Unlock()
		run()
	}

	var ix *index.Indexer
	if !readOnly {
		ix, err = newIndexer(app, cfg)
		if err != nil {
			return err
		}
	}

	// startWatcher spawns one debounced re-index watcher for a root. Used
	// for every startup root and for roots added mid-session by
	// index_folder. Prune is centralized on the first startup watcher only
	// (disablePrune elsewhere) per §7.4.
	startWatcher := func(r mcpRoot, wsID int64, disablePrune bool) {
		w := &watch.Watcher{
			Indexer:      ix,
			RootPath:     r.Path,
			WorkspaceID:  wsID,
			DisablePrune: disablePrune,
			Serialize:    serialize,
		}
		if worker != nil {
			w.OnFlush = worker.Notify
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, werr := w.Run(ctx); werr != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "warning: watcher %s: %v\n", w.RootPath, werr)
			}
		}()
	}

	server := newMCPServer(mcpDeps{
		app:          app,
		cfg:          cfg,
		state:        state,
		rs:           rs,
		worker:       worker,
		flt:          newPathFilter(cfg),
		ix:           ix,
		serialize:    serialize,
		startWatcher: startWatcher,
	})

	if readOnly {
		// Serving without the writer lock: search/read work off the existing
		// index (loaded once at startup — the concurrent writer's later
		// updates aren't visible until restart). For roots the index doesn't
		// cover yet, mark Phase A permanently "running" so search_documents
		// supplements with a live scan and flags results as partial — this
		// process can never index them itself.
		for _, r := range roots {
			ws, werr := app.DB.GetWorkspaceByName(r.Name)
			if werr != nil {
				continue
			}
			covered := false
			if ws != nil {
				if docs, derr := app.DB.GetDocumentsByWorkspace(ws.ID); derr == nil && len(docs) > 0 {
					covered = true
				}
			}
			if !covered {
				state.setPhaseA(r.Name, true)
			}
		}
	} else {
		// (a) Phase A of each root, sequentially, so a big corpus doesn't
		// starve the others — each root's tool calls are already served
		// (FTS/tier-0-ish partial results) while later roots are still being
		// scanned.
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

		// (c) one watcher per root.
		for i, r := range roots {
			wsID, werr := app.DB.UpsertWorkspace(r.Name, r.Path)
			if werr != nil {
				fmt.Fprintf(os.Stderr, "warning: watch %s: %v\n", r.Name, werr)
				continue
			}
			startWatcher(r, wsID, i != 0)
		}
		if len(roots) > 0 {
			state.setWatching(true)
		}
	}

	mode := "read-write"
	if readOnly {
		mode = "read-only"
	}
	fmt.Fprintf(os.Stderr, "ney mcp: serving %d workspace(s) over stdio, %s (Ctrl+C or close stdin to stop)\n", len(roots), mode)

	runErr := server.Run(ctx, &mcp.StdioTransport{})

	// Shutdown: stop background goroutines, then flush + release the lock.
	stop()
	wg.Wait()
	if !readOnly {
		if ferr := app.Vectors.Flush(); ferr != nil {
			fmt.Fprintf(os.Stderr, "warning: flush vectors: %v\n", ferr)
		}
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}

// acquireWriterLock tries to take the single-writer lock. When another live
// ney process already holds it (a second MCP client spawning its own `ney
// mcp` is the common case — e.g. Claude Desktop and Claude Code at once),
// it returns readOnly=true with a nil lock instead of failing: the server
// then serves search/read from the existing index and skips everything that
// writes (indexing, embedding, watching). Any other Acquire error is fatal.
func acquireWriterLock(dir string) (lock *lockfile.Lock, readOnly bool, err error) {
	lock, err = lockfile.Acquire(dir)
	if err == nil {
		return lock, false, nil
	}
	var le *lockfile.LockedError
	if errors.As(err, &le) {
		fmt.Fprintf(os.Stderr,
			"ney mcp: writer lock held by pid %d (%s) — serving read-only: search/read work; no indexing, embedding, or watching. Index snapshot loaded at startup.\n",
			le.PID, le.Command)
		return nil, true, nil
	}
	return nil, false, err
}

// resolveMCPRoots turns --root flags (or, if none were given, every
// workspace already in the DB) into a resolved root list. A --root whose
// basename collides with an existing workspace bound to a *different*
// root_path is a hard error — UpsertWorkspace's ON CONFLICT(name) DO UPDATE
// would otherwise silently re-point that workspace's root_path (CLAUDE.md;
// design §7.2), which would orphan its already-indexed documents.
// In read-only mode nothing may be written, so the workspace upsert for
// --root paths is skipped — an unindexed --root is still served via live
// scan and read_document, it just has no persisted workspace row.
func resolveMCPRoots(db *store.DB, rawRoots []string, readOnly bool) ([]mcpRoot, error) {
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
		if !readOnly {
			if _, err := db.UpsertWorkspace(name, resolved); err != nil {
				return nil, err
			}
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

// rootSet is the live set of served roots. It starts as the startup roots
// and grows when index_folder adds a workspace mid-session — read_document
// containment and live-scan targeting consult snapshots of it, so a newly
// indexed folder becomes readable without a server restart.
type rootSet struct {
	mu    sync.Mutex
	roots []mcpRoot
}

func newRootSet(roots []mcpRoot) *rootSet {
	return &rootSet{roots: append([]mcpRoot(nil), roots...)}
}

func (rs *rootSet) snapshot() []mcpRoot {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]mcpRoot(nil), rs.roots...)
}

// add appends r unless a root with the same name is already present.
func (rs *rootSet) add(r mcpRoot) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, existing := range rs.roots {
		if existing.Name == r.Name {
			return
		}
	}
	rs.roots = append(rs.roots, r)
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
	// readOnly is set once at construction (never mutated), when the writer
	// lock was held by another process — index_status reports it so clients
	// can tell this server will never index/embed/watch.
	readOnly bool
	// discovered is the session allowlist for read_document: resolved paths
	// that a user-directed search_folder call surfaced. Files outside the
	// served roots are readable ONLY if they were discovered this way first
	// (and never if pathfilter denies them) — so the AI can follow up on a
	// search the user asked for, but can't cold-read arbitrary home files.
	discovered map[string]bool
}

func newServerState(names []string, readOnly bool) *serverState {
	s := &serverState{
		phaseA:     make(map[string]bool, len(names)),
		readOnly:   readOnly,
		discovered: make(map[string]bool),
	}
	for _, n := range names {
		s.phaseA[n] = false
	}
	return s
}

func (s *serverState) addDiscovered(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discovered[path] = true
}

func (s *serverState) isDiscovered(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discovered[path]
}

func (s *serverState) isReadOnly() bool { return s.readOnly }

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
