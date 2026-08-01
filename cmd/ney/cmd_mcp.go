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
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/watch"
	"github.com/spf13/cobra"
)

var flagMCPRoots []string

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run ney as an MCP server over stdio (get_context, list_projects, search_documents, read_document, remember, update_profile, index_status)",
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
	// Internal marks a root ney registers itself rather than one the user
	// pointed at (today: ~/.ney/memory, the `remember` tool's target). It
	// only affects where the pathfilter deny is evaluated FROM: everything
	// under $HOME is normally checked component-by-component from $HOME down
	// (so a root that got into the DB under a dot-directory can't decapitate
	// the dotfile rule — see excludedForClient), but ney's own memory root
	// lives under the dot-directory ~/.ney by design, so its files are
	// checked from the root itself. Containment is unaffected.
	Internal bool
}

// runMCP wires one long-lived server process: acquire the writer lock, open
// the DB once for the process's lifetime, register the 9
// MCP tools, and start serving over stdio immediately — Phase A indexing,
// background embedding, and filesystem watching all happen in goroutines
// alongside it, never blocking the first tool call.
//
// STDOUT HYGIENE: stdout is reserved for the MCP protocol end to end. This
// function and everything it calls (loadConfig, initApp,
// newIndexer, the watcher) only ever write diagnostics to
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

	app, err := initApp(cfg)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	roots, err := resolveMCPRoots(app.DB, flagMCPRoots, readOnly)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "ney mcp: no workspaces to serve yet — pass --root <path>, or run `ney index` first")
	}

	// The memory workspace (~/.ney/memory, written by the `remember` tool) is
	// always served: read_document/search must be able to reach it in both
	// read-write and read-only mode, and — write mode only — it's indexed
	// and watched exactly like any other root, through the same per-root
	// pipeline below. It's registered here directly, not through
	// index_folder's home-directory validation, and doesn't count toward the
	// "no workspaces to serve yet" hint above. A prior run may already have
	// persisted it as a DB workspace (resolveMCPRoots would then have
	// returned it above) — skip re-adding it in that case.
	memPath := memoryDir()
	if err := os.MkdirAll(memPath, 0700); err != nil {
		return fmt.Errorf("create memory dir: %w", err)
	}
	// Symlink-resolve it for the same reason read_document does: containment
	// compares against the resolved path, and on macOS $HOME itself can sit
	// behind a symlink (/var -> /private/var).
	memPath = resolveRootBestEffort(memPath)
	if !hasRootNamed(roots, "memory") {
		roots = append(roots, mcpRoot{Name: "memory", Path: memPath, Internal: true})
	}
	// A prior run persisted ~/.ney/memory as a DB workspace, so resolveMCPRoots
	// returned it above without the Internal bit. Set it here: that bit is what
	// exempts memory files from the from-$HOME dotfile evaluation (~/.ney is a
	// dot-directory, so without it every remembered file becomes unreadable).
	for i := range roots {
		if roots[i].Path == memPath {
			roots[i].Internal = true
		}
	}

	state := newServerState(rootNames(roots), readOnly)
	rs := newRootSet(roots)

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
		flt:          newPathFilter(cfg),
		ix:           ix,
		serialize:    serialize,
		startWatcher: startWatcher,
		// scanCache lives on this long-running process only — get_context and
		// list_projects called close together reuse one ScanRepos pass instead
		// of forking git 3x per repo twice. CLI one-shot paths (e.g.
		// internal/discover, ney init's wizard) call ScanRepos directly and
		// never see this cache, so staleness is never a concern there.
		scanCache: &neycontext.ScanCache{},
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
			}
		}()

		// (b) one watcher per root.
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

	// Shutdown: stop background goroutines, then release the lock (deferred).
	stop()
	wg.Wait()

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

// hasRootNamed reports whether roots already contains an entry with the
// given name.
func hasRootNamed(roots []mcpRoot, name string) bool {
	for _, r := range roots {
		if r.Name == name {
			return true
		}
	}
	return false
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
	// discoveredOrder tracks insertion order of discovered's keys so it can
	// be capped at discoveredCap with FIFO eviction — otherwise a
	// long-running `ney mcp` process (days/weeks of uptime) would grow this
	// map without bound as search_folder gets called over and over.
	discoveredOrder []string
	// readCache holds the most recently read file, so read_document's
	// offset_chars/max_chars pagination doesn't re-read the whole document
	// per window. Exactly one entry: pagination walks a single file, so a
	// second slot buys nothing and each one can hold up to maxReadFileSize.
	readCache readCacheEntry
}

// readCacheEntry is the single-file memo behind read_document. info is the
// stat of the descriptor the content was actually read from — a hit requires
// os.SameFile against the caller's freshly-validated stat, so the cache can
// never serve content for a path that has since been re-pointed at a
// different file.
type readCacheEntry struct {
	path    string
	info    os.FileInfo
	content string
}

// cachedFile returns the memoized content for resolved when it is still the
// same file the caller just validated.
func (s *serverState) cachedFile(resolved string, validated os.FileInfo) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.readCache
	if e.info == nil || e.path != resolved {
		return "", false
	}
	// Identity first (same inode), then freshness: a file rewritten in place
	// keeps its identity but must not be served stale.
	if !os.SameFile(e.info, validated) {
		return "", false
	}
	if e.info.Size() != validated.Size() || !e.info.ModTime().Equal(validated.ModTime()) {
		return "", false
	}
	return e.content, true
}

// putCachedFile replaces the memo, dropping whatever file was there before.
func (s *serverState) putCachedFile(resolved string, info os.FileInfo, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCache = readCacheEntry{path: resolved, info: info, content: content}
}

// discoveredCap bounds serverState.discovered so a long-running server's
// memory doesn't grow unboundedly with every search_folder hit over its
// lifetime. Eviction is oldest-first: once the cap is hit, the
// least-recently-discovered path is forgotten to make room for a new one.
// Per CLAUDE.md, this set only gates read_document's fallback path for
// search_folder-surfaced files — eviction just means the user re-runs
// search_folder for that path; it never weakens the containment or
// pathfilter checks that gate every other read.
const discoveredCap = 500

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
	if s.discovered[path] {
		return
	}
	if len(s.discoveredOrder) >= discoveredCap {
		oldest := s.discoveredOrder[0]
		s.discoveredOrder = s.discoveredOrder[1:]
		delete(s.discovered, oldest)
	}
	s.discovered[path] = true
	s.discoveredOrder = append(s.discoveredOrder, path)
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
