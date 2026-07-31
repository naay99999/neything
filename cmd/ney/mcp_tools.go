package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/citation"
	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/pathfilter"
	"github.com/naay99999/neything/internal/scan"
	"github.com/naay99999/neything/internal/search"
)

// memoryDir is where the `remember` tool writes memory files, and the root
// runMCP registers as the always-served, always-indexed "memory" workspace
// (see cmd_mcp.go's runMCP). One md file per memory (see internal/context's
// WriteMemory), watched and indexed like any other workspace once a writer
// holds the lock.
func memoryDir() string {
	return filepath.Join(config.NeyDir(), "memory")
}

// maxReadFileSize bounds read_document's direct disk read so a stray
// multi-GB file under a watched root can't be read into memory whole by an
// MCP client.
const maxReadFileSize = 20 * 1024 * 1024

const (
	defaultSearchTopK = 8
	maxSearchTopK     = 50
	defaultReadChars  = 50000
	maxReadChars      = 200000
)

// mcpDeps bundles everything the tool handlers need. ix/serialize/
// startWatcher are nil in read-only mode (index_folder then always errors).
type mcpDeps struct {
	app          *AppState
	cfg          *config.Config
	state        *serverState
	rs           *rootSet
	worker       *index.EmbedWorker
	flt          *pathfilter.Filter
	ix           *index.Indexer
	serialize    func(func())
	startWatcher func(r mcpRoot, wsID int64, disablePrune bool)
	// scanCache memoizes buildProjects' ScanRepos call for a short TTL, so
	// get_context immediately followed by list_projects (or vice versa)
	// reuses one scan instead of re-forking git per repo twice. nil is valid
	// (falls back to an uncached scan every call) — tests that don't care
	// about this optimization can leave it unset.
	scanCache *neycontext.ScanCache
}

// newMCPServer builds the MCP server and registers all 9 tools (get_context,
// list_projects, remember, update_profile, search_documents, search_folder,
// read_document, index_folder, index_status). Factored out of runMCP so
// tests can construct one against an in-memory transport
// (mcp.NewInMemoryTransports) without going through stdio or the CLI's
// lockfile/signal-handling — see mcp_test.go.
func newMCPServer(deps mcpDeps) *mcp.Server {
	app, cfg, state, worker, flt := deps.app, deps.cfg, deps.state, deps.worker, deps.flt
	server := mcp.NewServer(&mcp.Implementation{Name: "ney", Version: Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_context",
		Description: "Call this FIRST, at the start of a session: a small markdown blob with who the user is " +
			"(their profile), which projects they've worked on recently (git activity across dev_roots + indexed " +
			"workspaces), and how to dig deeper. Cheap, never fails.",
	}, getContextHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_projects",
		Description: "Full detail for every known project (git repos under dev_roots, plus every indexed " +
			"workspace): path, branch, dirty state, last commit time + subject, indexed?, document/chunk counts. " +
			"Use this when get_context's one-line-per-project summary isn't enough.",
	}, listProjectsHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "remember",
		Description: "Save a fact or decision to memory (~/.ney/memory), so it's still known in future sessions " +
			"and across every connected AI client. Server derives the filename (date + sanitized title slug) — " +
			"no path argument. Becomes searchable via search_documents within moments (works even when this " +
			"server is read-only).",
	}, rememberHandler())

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_profile",
		Description: "Propose an edit to one section of the user's profile (~/.ney/profile.md), which get_context " +
			"reads every session. Replaces the named section's content, or appends to it if append=true; creates " +
			"the section if it doesn't exist yet. Works even when this server is read-only.",
	}, updateProfileHandler())

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_documents",
		Description: "Search the indexed local documents (hybrid keyword + semantic, mode depends on config). " +
			"Results may be partial while background indexing/embedding is still in progress — check the " +
			"returned index_status (phase_a_running, embedding_state) before concluding something isn't indexed. " +
			"While a root's initial scan is still running, results are supplemented with a live filesystem scan " +
			"(source: \"live-scan\") so filename/content matches show up even before that root finishes indexing. " +
			"If nothing relevant is found here, do not give up: ask the user WHERE the file might live " +
			"(Downloads? Desktop? Documents? a specific project folder?) and then call search_folder on that folder.",
	}, searchDocumentsHandler(app, cfg, state, worker, deps.rs, flt))

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_folder",
		Description: "Permanently index ONE folder (under the user's home or iCloud Drive) so its markdown/text " +
			"contents become fully searchable from now on, for every connected AI client. " +
			"Use when the user wants a folder searchable long-term (\"index โฟลเดอร์นี้\", deep/recurring " +
			"search needs); for a quick one-off lookup use search_folder instead. Indexing runs synchronously " +
			"and may take a while on large folders. Unavailable while another ney process holds the writer lock.",
	}, indexFolderHandler(deps))

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_folder",
		Description: "Live-scan ONE folder anywhere under the user's home directory for filename/content matches — " +
			"no indexing needed, bounded (~10k files / 2s), secret and hidden files are never returned. " +
			"Use this as the fallback when search_documents finds nothing: first ASK THE USER where the file " +
			"might be (e.g. ~/Downloads, ~/Desktop, ~/Documents, or a project folder they name), then call this " +
			"with that folder — do not guess-scan many folders unprompted. Files found here become readable " +
			"via read_document for the rest of this session.",
	}, searchFolderHandler(state, flt))

	mcp.AddTool(server, &mcp.Tool{
		Name: "read_document",
		Description: "Read the text of one file (.md/.markdown/.txt), windowed by offset_chars/max_chars, " +
			"read directly from disk (size-capped). Allowed paths: inside a known workspace root, or " +
			"files previously surfaced by a search_folder call this session; hidden files and secret-looking " +
			"files (.env, keys, credentials, ...) are never served.",
	}, readDocumentHandler(app, deps.rs, flt, state))

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_status",
		Description: "Report global indexing/embedding status: per-workspace counts, whether the embedder is " +
			"configured, embedding progress/state, which roots are still on their initial scan, and whether " +
			"filesystem watching is active. Cheap to poll instead of waiting for progress notifications.",
	}, indexStatusHandler(app, cfg, state, worker))

	return server
}

// --- search_documents --------------------------------------------------------

type searchDocumentsInput struct {
	Query      string `json:"query" jsonschema:"the search query"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"limit results to this workspace name (default: search all workspaces)"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema:"limit results to document paths starting with this prefix"`
	TopK       int    `json:"top_k,omitempty" jsonschema:"max results to return (default 8, max 50)"`
}

type searchResultItem struct {
	Path      string  `json:"path"`
	Workspace string  `json:"workspace"`
	Score     float32 `json:"score"`
	Snippet   string  `json:"snippet"`
	Location  string  `json:"location,omitempty"`
	Source    string  `json:"source"`
}

type indexStatusBrief struct {
	Chunks         int    `json:"chunks"`
	Vectors        int    `json:"vectors"`
	EmbeddingState string `json:"embedding_state"`
	PhaseARunning  bool   `json:"phase_a_running"`
}

type searchDocumentsOutput struct {
	Results     []searchResultItem `json:"results"`
	Meta        search.SearchMeta  `json:"meta"`
	IndexStatus indexStatusBrief   `json:"index_status"`
}

func searchDocumentsHandler(app *AppState, cfg *config.Config, state *serverState, worker *index.EmbedWorker, rs *rootSet, flt *pathfilter.Filter) mcp.ToolHandlerFor[searchDocumentsInput, searchDocumentsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchDocumentsInput) (*mcp.CallToolResult, searchDocumentsOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, searchDocumentsOutput{}, fmt.Errorf("query is required")
		}
		topK := in.TopK
		if topK <= 0 {
			topK = defaultSearchTopK
		}
		if topK > maxSearchTopK {
			topK = maxSearchTopK
		}

		opts := search.RetrieveOptions{
			TopK:       topK,
			FetchK:     config.FetchK(cfg, topK),
			Workspace:  in.Workspace,
			PathPrefix: in.PathPrefix,
			Mode:       cfg.Retrieval.Mode,
			Rerank:     cfg.Retrieval.Rerank,
		}
		retriever := &search.Retriever{DB: app.DB, Vectors: app.Vectors, Embedder: app.Embedder, Reranker: app.Reranker}
		results, meta, err := retriever.Search(ctx, in.Query, opts)
		if err != nil {
			return nil, searchDocumentsOutput{}, err
		}

		items := make([]searchResultItem, len(results))
		for i, r := range results {
			items[i] = searchResultItem{
				Path:      r.DocPath,
				Workspace: r.Workspace,
				Score:     r.Score,
				Snippet:   truncate(r.Content, 300),
				Location:  citation.FormatLocation(r.DocType, r.StartPos, r.EndPos),
				Source:    "index",
			}
		}
		items = appendLiveScanHits(ctx, items, in.Query, in.Workspace, rs.snapshot(), state, flt)

		chunkCount, _ := app.DB.CountChunks()
		out := searchDocumentsOutput{
			Results: items,
			Meta:    meta,
			IndexStatus: indexStatusBrief{
				Chunks:         chunkCount,
				Vectors:        app.Vectors.Count(),
				EmbeddingState: embeddingStatusFor(cfg, worker).State,
				PhaseARunning:  state.anyPhaseARunning(),
			},
		}

		var b strings.Builder
		if len(items) == 0 {
			b.WriteString("No results found.")
		} else {
			fmt.Fprintf(&b, "%d result(s):\n", len(items))
			for i, it := range items {
				loc := it.Location
				if loc != "" {
					loc = " (" + loc + ")"
				}
				tag := ""
				if it.Source == "live-scan" {
					tag = " [live-scan]"
				}
				fmt.Fprintf(&b, "%d. %s%s%s [%s] score=%.4f\n   %s\n", i+1, it.Path, loc, tag, it.Workspace, it.Score, it.Snippet)
			}
		}
		if meta.Degraded != "" {
			fmt.Fprintf(&b, "\nnote: %s\n", meta.Degraded)
		}
		if out.IndexStatus.PhaseARunning {
			b.WriteString("\nnote: indexing is still in progress — results may be partial.\n")
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, out, nil
	}
}

// appendLiveScanHits runs a tier-0 filesystem scan (internal/scan) on any
// root whose Phase A indexing is still in flight and merges its hits after
// the index results, deduped by path (an index hit always wins — it's
// strictly more informative), tagged source "live-scan". This is the MCP
// side of the design's §6.1 "index ready?" criterion: unlike the CLI (which
// has no server state and falls back to a path/document-count heuristic —
// see cmd_search.go's liveScanRoot), `ney mcp` knows exactly which roots
// haven't finished their initial scan yet.
func appendLiveScanHits(ctx context.Context, items []searchResultItem, query, workspace string, roots []mcpRoot, state *serverState, flt *pathfilter.Filter) []searchResultItem {
	targets := relevantScanRoots(workspace, roots, state)
	if len(targets) == 0 {
		return items
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[it.Path] = true
	}
	for _, r := range targets {
		hits, _, err := scan.Scan(ctx, r.Path, query, scan.Options{Exclude: flt})
		if err != nil {
			continue
		}
		for _, h := range hits {
			if seen[h.Path] {
				continue
			}
			seen[h.Path] = true
			snippet := h.Snippet
			if snippet == "" {
				snippet = "(filename match)"
			}
			items = append(items, searchResultItem{
				Path:      h.Path,
				Workspace: r.Name,
				Score:     float32(h.Score),
				Snippet:   snippet,
				Source:    "live-scan",
			})
		}
	}
	return items
}

// relevantScanRoots returns the roots a live scan should cover for this
// query: just the requested workspace's root if it's still on its initial
// Phase A scan, or every still-scanning root when the query isn't scoped to
// one workspace. Returns nil (no scan) once every root has finished Phase A.
func relevantScanRoots(workspace string, roots []mcpRoot, state *serverState) []mcpRoot {
	running := make(map[string]bool)
	for _, name := range state.runningRoots() {
		running[name] = true
	}
	if len(running) == 0 {
		return nil
	}
	if workspace != "" {
		for _, r := range roots {
			if r.Name == workspace && running[r.Name] {
				return []mcpRoot{r}
			}
		}
		return nil
	}
	var targets []mcpRoot
	for _, r := range roots {
		if running[r.Name] {
			targets = append(targets, r)
		}
	}
	return targets
}

// --- search_folder -------------------------------------------------------------

type searchFolderInput struct {
	Path  string `json:"path" jsonschema:"folder to scan (absolute or ~-relative; must be under the user's home directory)"`
	Query string `json:"query" jsonschema:"the search query (filename tokens + content grep for small plain-text files)"`
}

type searchFolderOutput struct {
	Results   []searchResultItem `json:"results"`
	Truncated bool               `json:"truncated"` // scan hit a file/time cap — there may be more
}

// searchFolderHandler is the user-directed whole-machine fallback: scan any
// folder under $HOME with the same bounded, secret-blind tier-0 scanner the
// server already uses for still-indexing roots. Hits are recorded in the
// session's discovered set so read_document can serve them afterwards.
func searchFolderHandler(state *serverState, flt *pathfilter.Filter) mcp.ToolHandlerFor[searchFolderInput, searchFolderOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchFolderInput) (*mcp.CallToolResult, searchFolderOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, searchFolderOutput{}, fmt.Errorf("query is required")
		}
		if strings.TrimSpace(in.Path) == "" {
			return nil, searchFolderOutput{}, fmt.Errorf("path is required")
		}

		dir, err := resolveAllowedScanDir(in.Path)
		if err != nil {
			return nil, searchFolderOutput{}, err
		}

		hits, truncated, err := scan.Scan(ctx, dir, in.Query, scan.Options{Exclude: flt})
		if err != nil {
			return nil, searchFolderOutput{}, err
		}

		items := make([]searchResultItem, 0, len(hits))
		for _, h := range hits {
			state.addDiscovered(filepath.Clean(h.Path))
			snippet := h.Snippet
			if snippet == "" {
				snippet = "(filename match)"
			}
			items = append(items, searchResultItem{
				Path:    h.Path,
				Score:   float32(h.Score),
				Snippet: snippet,
				Source:  "live-scan",
			})
		}

		out := searchFolderOutput{Results: items, Truncated: truncated}
		var b strings.Builder
		if len(items) == 0 {
			fmt.Fprintf(&b, "No matches in %s.", dir)
		} else {
			fmt.Fprintf(&b, "%d match(es) in %s:\n", len(items), dir)
			for i, it := range items {
				fmt.Fprintf(&b, "%d. %s score=%.2f\n   %s\n", i+1, it.Path, it.Score, it.Snippet)
			}
			b.WriteString("\nThese files are now readable via read_document.\n")
		}
		if truncated {
			b.WriteString("note: scan was truncated (folder too large) — results may be incomplete; try a more specific subfolder.\n")
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, out, nil
	}
}

// --- index_folder --------------------------------------------------------------

type indexFolderInput struct {
	Path string `json:"path" jsonschema:"folder to index permanently (absolute or ~-relative; must be under the user's home directory or iCloud Drive)"`
}

type indexFolderOutput struct {
	Workspace    string `json:"workspace"`
	FilesScanned int    `json:"files_scanned"`
	Chunks       int    `json:"chunks"`
	DurationMS   int64  `json:"duration_ms"`
}

// indexFolderHandler indexes a folder into a permanent workspace
// mid-session: served to every AI client from now on (the workspace table is
// the source of truth), readable immediately (dynamic root set), watched for
// changes for the rest of this session.
func indexFolderHandler(deps mcpDeps) mcp.ToolHandlerFor[indexFolderInput, indexFolderOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in indexFolderInput) (*mcp.CallToolResult, indexFolderOutput, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, indexFolderOutput{}, fmt.Errorf("path is required")
		}
		if deps.state.isReadOnly() || deps.ix == nil {
			return nil, indexFolderOutput{}, fmt.Errorf(
				"this server is read-only (another ney process holds the writer lock) — close it and retry, or use search_folder for a one-off scan")
		}

		dir, err := resolveAllowedScanDir(in.Path)
		if err != nil {
			return nil, indexFolderOutput{}, err
		}

		name := filepath.Base(dir)
		existing, err := deps.app.DB.GetWorkspaceByName(name)
		if err != nil {
			return nil, indexFolderOutput{}, err
		}
		if existing != nil && resolveRootBestEffort(existing.RootPath) != dir {
			return nil, indexFolderOutput{}, fmt.Errorf(
				"workspace %q is already bound to %s — index it from the CLI with `ney index %s --workspace <other-name>` instead",
				name, existing.RootPath, in.Path)
		}

		wsID, err := deps.app.DB.UpsertWorkspace(name, dir)
		if err != nil {
			return nil, indexFolderOutput{}, err
		}

		deps.state.setPhaseA(name, true)
		var stats *index.Stats
		var ierr error
		deps.serialize(func() {
			stats, ierr = deps.ix.Index(ctx, dir, name)
		})
		deps.state.setPhaseA(name, false)
		if ierr != nil {
			return nil, indexFolderOutput{}, fmt.Errorf("index %s: %w", dir, ierr)
		}
		if deps.worker != nil {
			deps.worker.Notify()
		}

		root := mcpRoot{Name: name, Path: dir}
		deps.rs.add(root)
		if deps.startWatcher != nil {
			deps.startWatcher(root, wsID, true)
		}

		out := indexFolderOutput{
			Workspace:    name,
			FilesScanned: stats.FilesScanned,
			Chunks:       stats.ChunksCreated,
			DurationMS:   stats.Duration.Milliseconds(),
		}
		text := fmt.Sprintf("Indexed %s as workspace %q: %d files, %d chunks (%.1fs). "+
			"Its contents are now searchable via search_documents and readable via read_document — for every connected client, permanently.",
			dir, name, out.FilesScanned, out.Chunks, stats.Duration.Seconds())
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

// resolveAllowedScanDir resolves a client-supplied folder and requires it to
// be inside the user's home directory (which on macOS includes iCloud Drive,
// under ~/Library/Mobile Documents) — search_folder/index_folder may roam
// the user's files when the user directs them there, but never system paths,
// other users' homes, or mounted volumes.
func resolveAllowedScanDir(raw string) (string, error) {
	p := expandTilde(raw)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		p = abs
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("folder not found: %s", raw)
	}
	resolved = filepath.Clean(resolved)

	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("folder not found: %s", raw)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a folder: %s", raw)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	home = resolveRootBestEffort(home)
	if resolved != home && !strings.HasPrefix(resolved, home+string(filepath.Separator)) {
		return "", fmt.Errorf("folder outside the home directory is not allowed: %s", raw)
	}
	return resolved, nil
}

// --- read_document ------------------------------------------------------------

type readDocumentInput struct {
	Path        string `json:"path" jsonschema:"absolute or ~-relative path to the file (must be inside an indexed workspace root)"`
	OffsetChars int    `json:"offset_chars,omitempty" jsonschema:"character offset to start reading from (default 0)"`
	MaxChars    int    `json:"max_chars,omitempty" jsonschema:"max characters to return (default 50000, max 200000)"`
}

type readDocumentOutput struct {
	Content    string `json:"content"`
	TotalChars int    `json:"total_chars"`
	Truncated  bool   `json:"truncated"`
	NextOffset int    `json:"next_offset,omitempty"`
	Source     string `json:"source"` // always "file" — every supported format is read directly off disk
}

func readDocumentHandler(app *AppState, rs *rootSet, flt *pathfilter.Filter, state *serverState) mcp.ToolHandlerFor[readDocumentInput, readDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in readDocumentInput) (*mcp.CallToolResult, readDocumentOutput, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, readDocumentOutput{}, fmt.Errorf("path is required")
		}
		resolved, root, err := resolveAllowedPath(in.Path, rs.snapshot())
		if err != nil {
			// Outside every served root — still allowed IF a user-directed
			// search_folder call surfaced this exact file earlier in the
			// session (checked against home so the secret deny still applies
			// to every path component below it).
			var derr error
			if resolved, derr = resolveDiscoveredPath(in.Path, state); derr != nil {
				return nil, readDocumentOutput{}, err
			}
			home, herr := os.UserHomeDir()
			if herr != nil {
				return nil, readDocumentOutput{}, err
			}
			root = mcpRoot{Name: "home", Path: resolveRootBestEffort(home)}
		}
		if flt.ExcludedPath(root.Path, resolved) {
			return nil, readDocumentOutput{}, fmt.Errorf("path is excluded by security policy (hidden or secret file): %s", in.Path)
		}

		maxChars := in.MaxChars
		if maxChars <= 0 {
			maxChars = defaultReadChars
		}
		if maxChars > maxReadChars {
			maxChars = maxReadChars
		}

		full, source, err := readPlainTextFile(resolved)
		if err != nil {
			return nil, readDocumentOutput{}, err
		}

		content, total, truncated, next := windowContent(full, in.OffsetChars, maxChars)
		out := readDocumentOutput{
			Content:    content,
			TotalChars: total,
			Truncated:  truncated,
			NextOffset: next,
			Source:     source,
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: content}}}, out, nil
	}
}

// readPlainTextFile reads a file directly off disk — every supported format
// (.md/.markdown/.txt) is plain text, so this is the only read path.
func readPlainTextFile(resolved string) (content, source string, err error) {
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("path not found: %s", resolved)
	}
	if fi.IsDir() {
		return "", "", fmt.Errorf("path is a directory: %s", resolved)
	}
	if fi.Size() > maxReadFileSize {
		return "", "", fmt.Errorf("file too large to read (%d bytes, max %d)", fi.Size(), maxReadFileSize)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	return string(data), "file", nil
}

// resolveDiscoveredPath resolves rawPath and returns it only if it is in the
// session's discovered set (surfaced by a prior user-directed search_folder
// call). Symlink resolution matches how search_folder recorded the hit, so a
// symlink planted at a discovered path can't be swapped to point elsewhere —
// the post-resolution string must still be the recorded one.
func resolveDiscoveredPath(rawPath string, state *serverState) (string, error) {
	p := expandTilde(rawPath)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", rawPath)
	}
	resolved = filepath.Clean(resolved)
	if !state.isDiscovered(resolved) {
		return "", fmt.Errorf("not a discovered path: %s", rawPath)
	}
	return resolved, nil
}

// resolveAllowedPath expands ~, makes rawPath absolute, resolves symlinks on
// both it and every known root, and only allows it through if it falls
// inside one of those resolved roots (also returning which root matched, so
// the caller can apply per-root policy like the pathfilter deny). Symlink
// resolution is required on both sides of the comparison: a symlink planted
// inside an allowed root could otherwise point anywhere on disk and slip
// past a plain prefix check.
func resolveAllowedPath(rawPath string, roots []mcpRoot) (string, mcpRoot, error) {
	p := expandTilde(rawPath)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", mcpRoot{}, fmt.Errorf("resolve path: %w", err)
		}
		p = abs
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", mcpRoot{}, fmt.Errorf("path not found: %s", rawPath)
	}
	resolved = filepath.Clean(resolved)

	for _, r := range roots {
		if resolved == r.Path || strings.HasPrefix(resolved, r.Path+string(filepath.Separator)) {
			return resolved, r, nil
		}
	}
	return "", mcpRoot{}, fmt.Errorf("path outside indexed workspaces: %s", rawPath)
}

// windowContent slices full (by rune, i.e. by character, matching the
// offset_chars/max_chars naming) starting at offset for up to maxChars
// characters, reporting the total length, whether more remains, and the
// offset a follow-up call should use to continue.
//
// This decodes full incrementally via utf8.DecodeRuneInString, tracking
// byte offsets, instead of allocating a []rune copy of the whole string —
// for a large file (read_document caps single reads at maxReadFileSize) that
// conversion would allocate up to ~4x the file's size (each rune is an
// int32) on every single paginated call, even when only a small window is
// requested. The returned content is a zero-copy substring of full (Go
// string slicing shares the backing array, no allocation). Computing an
// exact total still requires visiting every byte once — same as before —
// but with no per-call rune-array allocation and no second copy to rebuild
// the windowed string.
func windowContent(full string, offset, maxChars int) (content string, total int, truncated bool, nextOffset int) {
	if offset < 0 {
		offset = 0
	}
	if maxChars < 0 {
		maxChars = 0
	}
	end := offset + maxChars

	startByte, endByte := -1, -1
	runeCount := 0
	byteOffset := 0
	for byteOffset < len(full) {
		if runeCount == offset {
			startByte = byteOffset
		}
		if runeCount == end {
			endByte = byteOffset
		}
		_, size := utf8.DecodeRuneInString(full[byteOffset:])
		byteOffset += size
		runeCount++
	}
	total = runeCount
	// A target at or beyond EOF (offset/end >= total) never gets set inside
	// the loop above — clamp it to EOF, matching the old []rune-slice
	// clamping behavior (runes[total:] == "").
	if startByte == -1 {
		startByte = len(full)
	}
	if endByte == -1 {
		endByte = len(full)
	}

	content = full[startByte:endByte]
	truncated = end < total
	if truncated {
		nextOffset = end
	}
	return content, total, truncated, nextOffset
}

// --- get_context / list_projects: shared project-set assembly ------------------

type workspaceInfo struct {
	Name          string  `json:"name"`
	RootPath      string  `json:"root_path"`
	Documents     int     `json:"documents"`
	Chunks        int     `json:"chunks"`
	EmbedCoverage float64 `json:"embed_coverage"`
}

// buildProjects assembles the project set both get_context and list_projects
// render from: every git repo found live under cfg.Context.DevRoots, unioned
// by path with every workspace root already in the DB — so a workspace that
// isn't a git repo (a plain docs folder, or the built-in "memory" workspace)
// still shows up as a project, just without branch/commit info. A DB
// workspace whose root_path matches a scanned repo's path is folded into
// that repo's entry rather than duplicated.
//
// Indexed is set per the *current* served root set (rs.snapshot()), not the
// DB snapshot, so it reflects roots index_folder added this session and
// (in read-only mode) never flips true for roots this process can't index.
//
// A DB error degrades rather than aborting: the caller gets the scanned
// repos plus a non-nil error to decide how to report it (get_context must
// never fail; list_projects may surface it as a tool error).
//
// scanCache, when non-nil, memoizes the ScanRepos call for a short TTL so a
// get_context call immediately followed by list_projects (a common
// session-start pattern) reuses one scan instead of forking git 3x per repo
// twice over. A nil scanCache (e.g. in tests that don't wire one) just scans
// fresh every call — same behavior as before this cache existed.
func buildProjects(ctx context.Context, cfg *config.Config, app *AppState, rs *rootSet, scanCache *neycontext.ScanCache) ([]neycontext.Project, error) {
	var projects []neycontext.Project
	if scanCache != nil {
		projects = scanCache.Get(ctx, cfg.Context.DevRoots)
	} else {
		projects = neycontext.ScanRepos(ctx, cfg.Context.DevRoots)
	}
	byPath := make(map[string]bool, len(projects))
	for _, p := range projects {
		byPath[filepath.Clean(p.Path)] = true
	}

	workspaces, dberr := app.DB.ListWorkspaces()
	for _, ws := range workspaces {
		path := filepath.Clean(ws.RootPath)
		if byPath[path] {
			continue // already represented by a scanned repo at this path
		}
		byPath[path] = true
		projects = append(projects, neycontext.Project{Name: ws.Name, Path: path})
	}

	roots := rs.snapshot()
	for i := range projects {
		projects[i].Indexed = coveredByRoot(projects[i].Path, roots)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastCommit.After(projects[j].LastCommit)
	})
	return projects, dberr
}

// coveredByRoot reports whether path is exactly, or nested under, one of
// roots — the same containment test read_document uses (resolveAllowedPath),
// applied here to decide a project's Indexed flag against the live served
// root set.
func coveredByRoot(path string, roots []mcpRoot) bool {
	for _, r := range roots {
		if path == r.Path || strings.HasPrefix(path, r.Path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// --- get_context -----------------------------------------------------------------

type getContextInput struct{}

type getContextOutput struct {
	Context string `json:"context"`
}

// getContextHandler renders the Layer-1 bootstrap blob (design §"get_context
// output"): profile + active projects + how-to-dig-deeper. Per the design's
// "Layer 1 must never fail" principle, every failure path here degrades —
// a broken DB read or an unreadable profile is noted in the rendered text
// instead of returning an MCP error.
func getContextHandler(deps mcpDeps) mcp.ToolHandlerFor[getContextInput, getContextOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getContextInput) (*mcp.CallToolResult, getContextOutput, error) {
		var notes []string

		projects, err := buildProjects(ctx, deps.cfg, deps.app, deps.rs, deps.scanCache)
		if err != nil {
			notes = append(notes, fmt.Sprintf("note: could not list indexed workspaces (%v) — showing scanned repos only.", err))
		}

		profilePath := filepath.Join(config.NeyDir(), "profile.md")
		profile, created, perr := neycontext.LoadProfile(profilePath)
		if perr != nil {
			notes = append(notes, fmt.Sprintf("note: could not load profile.md (%v) — proceeding with an empty profile.", perr))
			profile = ""
		} else if created {
			notes = append(notes, fmt.Sprintf("note: no profile existed yet — created a starter template at %s; consider filling it in via update_profile.", profilePath))
		}

		activeDays := deps.cfg.Context.ActiveDays
		if activeDays <= 0 {
			activeDays = 14
		}
		text := neycontext.Render(profile, projects, activeDays, 10, time.Now())
		for _, n := range notes {
			text += "\n" + n + "\n"
		}

		out := getContextOutput{Context: text}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

// --- list_projects -----------------------------------------------------------------

type projectDetail struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	Branch            string `json:"branch,omitempty"`
	Dirty             bool   `json:"dirty"`
	LastCommit        string `json:"last_commit,omitempty"` // RFC3339; empty if unknown (not a git repo)
	LastCommitSubject string `json:"last_commit_subject,omitempty"`
	Indexed           bool   `json:"indexed"`
	Documents         int    `json:"documents"`
	Chunks            int    `json:"chunks"`
}

type listProjectsInput struct{}

type listProjectsOutput struct {
	Projects []projectDetail `json:"projects"`
}

// listProjectsHandler is the L1.5 detail view: the same project set as
// get_context, one full detail block per project, with per-workspace
// document/chunk counts folded in via computeWorkspaceInfo. Replaces the
// removed list_workspaces tool.
func listProjectsHandler(deps mcpDeps) mcp.ToolHandlerFor[listProjectsInput, listProjectsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ listProjectsInput) (*mcp.CallToolResult, listProjectsOutput, error) {
		projects, err := buildProjects(ctx, deps.cfg, deps.app, deps.rs, deps.scanCache)
		if err != nil {
			return nil, listProjectsOutput{}, err
		}
		// list_projects doesn't surface EmbedCoverage (only Documents/Chunks
		// are read below), so skip building the vector-ID set entirely — see
		// computeWorkspaceInfo's withCoverage param.
		wsInfo, err := computeWorkspaceInfo(deps.app, false)
		if err != nil {
			return nil, listProjectsOutput{}, err
		}
		docsByPath := make(map[string]workspaceInfo, len(wsInfo))
		for _, w := range wsInfo {
			docsByPath[filepath.Clean(w.RootPath)] = w
		}

		out := make([]projectDetail, 0, len(projects))
		var b strings.Builder
		fmt.Fprintf(&b, "%d project(s):\n", len(projects))
		for _, p := range projects {
			d := projectDetail{
				Name:              p.Name,
				Path:              p.Path,
				Branch:            p.Branch,
				Dirty:             p.Dirty,
				LastCommitSubject: p.LastCommitSubject,
				Indexed:           p.Indexed,
			}
			if !p.LastCommit.IsZero() {
				d.LastCommit = p.LastCommit.Format(time.RFC3339)
			}
			if w, ok := docsByPath[filepath.Clean(p.Path)]; ok {
				d.Documents = w.Documents
				d.Chunks = w.Chunks
			}
			out = append(out, d)

			fmt.Fprintf(&b, "- %s (%s)", d.Name, d.Path)
			if d.Branch != "" {
				fmt.Fprintf(&b, " · %s", d.Branch)
			}
			if d.Dirty {
				b.WriteString(" · dirty")
			}
			if d.LastCommit != "" {
				fmt.Fprintf(&b, " · last commit %s", d.LastCommit)
				if d.LastCommitSubject != "" {
					fmt.Fprintf(&b, ": %q", d.LastCommitSubject)
				}
			}
			if d.Indexed {
				fmt.Fprintf(&b, " · indexed (%d docs, %d chunks)", d.Documents, d.Chunks)
			} else {
				b.WriteString(" · not indexed")
			}
			b.WriteString("\n")
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, listProjectsOutput{Projects: out}, nil
	}
}

// --- remember ----------------------------------------------------------------------

type rememberInput struct {
	Title   string   `json:"title" jsonschema:"short title for this memory (used to derive the filename)"`
	Content string   `json:"content" jsonschema:"the fact or decision to remember"`
	Project string   `json:"project,omitempty" jsonschema:"optional project name this memory relates to"`
	Tags    []string `json:"tags,omitempty" jsonschema:"optional tags"`
}

type rememberOutput struct {
	Path string `json:"path"`
}

// rememberHandler writes a new memory file. It never touches the DB or
// vector store — only a plain md file under memoryDir() — so per the
// design's "Layer 1 must never fail" / read-only-mode reasoning it works
// identically whether this server holds the writer lock or not; indexing
// the new file is the lock holder's watcher's job.
func rememberHandler() mcp.ToolHandlerFor[rememberInput, rememberOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in rememberInput) (*mcp.CallToolResult, rememberOutput, error) {
		if strings.TrimSpace(in.Title) == "" {
			return nil, rememberOutput{}, fmt.Errorf("title is required")
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, rememberOutput{}, fmt.Errorf("content is required")
		}
		path, err := neycontext.WriteMemory(memoryDir(), neycontext.Memory{
			Title:   in.Title,
			Content: in.Content,
			Project: in.Project,
			Tags:    in.Tags,
			Date:    time.Now(),
		})
		if err != nil {
			return nil, rememberOutput{}, err
		}
		out := rememberOutput{Path: path}
		text := fmt.Sprintf("Saved memory to %s. It becomes searchable via search_documents shortly (the memory workspace is watched and indexed automatically).", path)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

// --- update_profile ------------------------------------------------------------------

type updateProfileInput struct {
	Section string `json:"section" jsonschema:"the profile section heading to edit (e.g. 'Current focus')"`
	Content string `json:"content" jsonschema:"the new section content"`
	Append  bool   `json:"append,omitempty" jsonschema:"append to the existing section body instead of replacing it (default: replace)"`
}

type updateProfileOutput struct {
	Path string `json:"path"`
}

// updateProfileHandler edits one named section of ~/.ney/profile.md. Like
// remember, it only ever touches that one plain md file — read-only-safe for
// the same reason.
func updateProfileHandler() mcp.ToolHandlerFor[updateProfileInput, updateProfileOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in updateProfileInput) (*mcp.CallToolResult, updateProfileOutput, error) {
		if strings.TrimSpace(in.Section) == "" {
			return nil, updateProfileOutput{}, fmt.Errorf("section is required")
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, updateProfileOutput{}, fmt.Errorf("content is required")
		}
		path := filepath.Join(config.NeyDir(), "profile.md")
		if err := neycontext.UpdateProfile(path, in.Section, in.Content, in.Append); err != nil {
			return nil, updateProfileOutput{}, err
		}
		out := updateProfileOutput{Path: path}
		text := fmt.Sprintf("Updated profile section %q at %s.", in.Section, path)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

// computeWorkspaceInfo builds document/chunk counts per workspace via
// SELECT COUNT(*) (internal/store/counts.go) instead of loading full
// document rows or chunk-ID slices just to take len() of them.
//
// When withCoverage is false (list_projects, which never reads
// EmbedCoverage from its output), counting is all that happens — no vector
// IDs are touched. When withCoverage is true (index_status, which reports
// "N% embedded"), app.Vectors.IDs() is fetched exactly once and reused as an
// in-memory set for every workspace's diff against its chunk IDs, per the
// design's "build vector ID set once per call; fine at current scale" note —
// avoiding an O(workspaces) number of full vector-store scans. Computing
// exact coverage still needs the actual chunk IDs (to diff against vector
// IDs), so that part falls back to GetChunkIDsByWorkspace regardless.
func computeWorkspaceInfo(app *AppState, withCoverage bool) ([]workspaceInfo, error) {
	workspaces, err := app.DB.ListWorkspaces()
	if err != nil {
		return nil, err
	}

	var vecIDs map[string]bool
	if withCoverage {
		vecIDs = make(map[string]bool, app.Vectors.Count())
		for _, id := range app.Vectors.IDs() {
			vecIDs[id] = true
		}
	}

	out := make([]workspaceInfo, 0, len(workspaces))
	for _, ws := range workspaces {
		docCount, err := app.DB.CountDocumentsByWorkspace(ws.ID)
		if err != nil {
			return nil, err
		}

		info := workspaceInfo{Name: ws.Name, RootPath: ws.RootPath, Documents: docCount}
		if withCoverage {
			chunkIDs, err := app.DB.GetChunkIDsByWorkspace(ws.ID)
			if err != nil {
				return nil, err
			}
			coverage := 0.0
			if len(chunkIDs) > 0 {
				embedded := 0
				for _, id := range chunkIDs {
					if vecIDs[strconv.FormatInt(id, 10)] {
						embedded++
					}
				}
				coverage = float64(embedded) / float64(len(chunkIDs))
			}
			info.Chunks = len(chunkIDs)
			info.EmbedCoverage = coverage
		} else {
			chunkCount, err := app.DB.CountChunksByWorkspace(ws.ID)
			if err != nil {
				return nil, err
			}
			info.Chunks = chunkCount
		}
		out = append(out, info)
	}
	return out, nil
}

// --- index_status --------------------------------------------------------------

type indexStatusInput struct{}

type embedderStatus struct {
	Configured bool   `json:"configured"`
	Model      string `json:"model,omitempty"`
}

type embeddingStatus struct {
	State string `json:"state"` // idle | running | backoff | blocked_mismatch | disabled
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type indexStatusOutput struct {
	// Mode is "read-write" normally, or "read-only" when another ney process
	// held the writer lock at startup — in read-only mode this server never
	// indexes, embeds, or watches, and serves the index as of startup.
	Mode          string          `json:"mode"`
	Workspaces    []workspaceInfo `json:"workspaces"`
	Embedder      embedderStatus  `json:"embedder"`
	Embedding     embeddingStatus `json:"embedding"`
	PhaseARunning []string        `json:"phase_a_running"`
	Watching      bool            `json:"watching"`
}

func indexStatusHandler(app *AppState, cfg *config.Config, state *serverState, worker *index.EmbedWorker) mcp.ToolHandlerFor[indexStatusInput, indexStatusOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ indexStatusInput) (*mcp.CallToolResult, indexStatusOutput, error) {
		infos, err := computeWorkspaceInfo(app, true)
		if err != nil {
			return nil, indexStatusOutput{}, err
		}

		embed := embedderStatus{Configured: cfg.HasEmbedder()}
		if embed.Configured {
			embed.Model = cfg.Embedder.Model
		}

		mode := "read-write"
		if state.isReadOnly() {
			mode = "read-only"
		}
		out := indexStatusOutput{
			Mode:          mode,
			Workspaces:    infos,
			Embedder:      embed,
			Embedding:     embeddingStatusFor(cfg, worker),
			PhaseARunning: state.runningRoots(),
			Watching:      state.isWatching(),
		}

		var b strings.Builder
		fmt.Fprintf(&b, "mode: %s\n", mode)
		fmt.Fprintf(&b, "embedder: configured=%v", embed.Configured)
		if embed.Model != "" {
			fmt.Fprintf(&b, " model=%s", embed.Model)
		}
		fmt.Fprintf(&b, "\nembedding: state=%s done=%d total=%d\n", out.Embedding.State, out.Embedding.Done, out.Embedding.Total)
		fmt.Fprintf(&b, "watching: %v\n", out.Watching)
		if len(out.PhaseARunning) > 0 {
			fmt.Fprintf(&b, "phase A still running for: %s\n", strings.Join(out.PhaseARunning, ", "))
		}
		for _, w := range infos {
			fmt.Fprintf(&b, "- %s: %d documents, %d chunks, %.0f%% embedded\n", w.Name, w.Documents, w.Chunks, w.EmbedCoverage*100)
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, out, nil
	}
}

// embeddingStatusFor reports the embed worker's state, or a fixed "disabled"
// (0/0) status when no embedder is configured at all — in that case there is
// no worker instance to poll (runMCP never constructs one).
func embeddingStatusFor(cfg *config.Config, worker *index.EmbedWorker) embeddingStatus {
	if worker == nil || !cfg.HasEmbedder() {
		return embeddingStatus{State: index.WorkerStateDisabled}
	}
	s := worker.Status()
	state := s.State
	if state == "" {
		state = index.WorkerStateIdle
	}
	return embeddingStatus{State: state, Done: s.Done, Total: s.Total}
}
