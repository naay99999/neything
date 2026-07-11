package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/citation"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/search"
)

// maxReadFileSize bounds both the direct-read (plain-text) and fresh-parse
// (unindexed pdf/docx) paths of read_document, mirroring the design's "size
// cap 20 MB" for fresh parses — applied uniformly so a stray multi-GB file
// under a watched root can't be read into memory whole by an MCP client.
const maxReadFileSize = 20 * 1024 * 1024

// freshParseTimeout bounds how long read_document's fresh-parse fallback
// (for a not-yet-indexed pdf/docx, which may run OCR) is allowed to block a
// tool call.
const freshParseTimeout = 10 * time.Second

const (
	defaultSearchTopK = 8
	maxSearchTopK     = 50
	defaultReadChars  = 50000
	maxReadChars      = 200000
)

// newMCPServer builds the MCP server and registers all 4 tools. Factored out
// of runMCP so tests can construct one against an in-memory transport
// (mcp.NewInMemoryTransports) without going through stdio or the CLI's
// lockfile/signal-handling — see mcp_test.go.
func newMCPServer(app *AppState, cfg *config.Config, state *serverState, roots []mcpRoot, worker *index.EmbedWorker) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "ney", Version: Version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_documents",
		Description: "Search the indexed local documents (hybrid keyword + semantic, mode depends on config). " +
			"Results may be partial while background indexing/embedding is still in progress — check the " +
			"returned index_status (phase_a_running, embedding_state) before concluding something isn't indexed.",
	}, searchDocumentsHandler(app, cfg, state, worker))

	mcp.AddTool(server, &mcp.Tool{
		Name: "read_document",
		Description: "Read the text of one indexed (or indexable) file, windowed by offset_chars/max_chars. " +
			"Plain-text formats (.md/.txt/.html/.json/...) are read directly from disk. PDF/DOCX are " +
			"reassembled from indexed chunk rows in order when available (approximate: chunk overlap can " +
			"duplicate ~150 chars at each join, and start/end positions are pages/paragraphs, not exact " +
			"char offsets) — or parsed fresh (size-capped, OCR time-boxed) if the file exists under an " +
			"indexed root but hasn't been indexed yet. Only paths inside a known workspace root are allowed.",
	}, readDocumentHandler(app, roots))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_workspaces",
		Description: "List every indexed workspace with document/chunk counts and embedding coverage (0..1).",
	}, listWorkspacesHandler(app))

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_status",
		Description: "Report global indexing/embedding status: per-workspace counts, whether the embedder is " +
			"configured, embedding progress/state, which roots are still on their initial scan, and whether " +
			"filesystem watching is active. Cheap to poll instead of waiting for progress notifications.",
	}, indexStatusHandler(app, cfg, state, roots, worker))

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

func searchDocumentsHandler(app *AppState, cfg *config.Config, state *serverState, worker *index.EmbedWorker) mcp.ToolHandlerFor[searchDocumentsInput, searchDocumentsOutput] {
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
				fmt.Fprintf(&b, "%d. %s%s [%s] score=%.4f\n   %s\n", i+1, it.Path, loc, it.Workspace, it.Score, it.Snippet)
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
	Source     string `json:"source"` // file | chunks | fresh-parse
}

func readDocumentHandler(app *AppState, roots []mcpRoot) mcp.ToolHandlerFor[readDocumentInput, readDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in readDocumentInput) (*mcp.CallToolResult, readDocumentOutput, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, readDocumentOutput{}, fmt.Errorf("path is required")
		}
		resolved, err := resolveAllowedPath(in.Path, roots)
		if err != nil {
			return nil, readDocumentOutput{}, err
		}

		maxChars := in.MaxChars
		if maxChars <= 0 {
			maxChars = defaultReadChars
		}
		if maxChars > maxReadChars {
			maxChars = maxReadChars
		}

		ext := strings.ToLower(filepath.Ext(resolved))
		isBinary := ext == ".pdf" || ext == ".docx"

		var full, source string
		if isBinary {
			full, source, err = readBinaryDocument(ctx, app, resolved)
		} else {
			full, source, err = readPlainTextFile(resolved)
		}
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

// readBinaryDocument returns the full text of a pdf/docx document, either
// reassembled from its indexed chunk rows (in chunk_index order — the
// approximate path the tool description warns about) or, if it hasn't been
// indexed yet, parsed fresh via the same loader registry `ney index` uses.
func readBinaryDocument(ctx context.Context, app *AppState, resolved string) (content, source string, err error) {
	doc, err := app.DB.GetDocumentByPath(resolved)
	if err != nil {
		return "", "", err
	}
	if doc != nil {
		chunks, err := app.DB.GetChunksByDocumentOrdered(doc.ID)
		if err != nil {
			return "", "", err
		}
		parts := make([]string, len(chunks))
		for i, c := range chunks {
			parts[i] = c.Content
		}
		return strings.Join(parts, "\n"), "chunks", nil
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("path not found: %s", resolved)
	}
	if fi.Size() > maxReadFileSize {
		return "", "", fmt.Errorf("file too large to parse fresh (%d bytes, max %d)", fi.Size(), maxReadFileSize)
	}

	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	reg := newLoaderRegistry(cfg)
	ld, ok := reg.Dispatch(resolved)
	if !ok {
		return "", "", fmt.Errorf("no loader available for %s", resolved)
	}

	parseCtx, cancel := context.WithTimeout(ctx, freshParseTimeout)
	defer cancel()
	docs, err := ld.Load(parseCtx, resolved)
	if err != nil {
		return "", "", fmt.Errorf("parse %s: %w", resolved, err)
	}
	parts := make([]string, len(docs))
	for i, d := range docs {
		parts[i] = d.Content
	}
	return strings.Join(parts, "\n"), "fresh-parse", nil
}

// readPlainTextFile reads a non-pdf/docx file directly off disk — the fast,
// exact path for markdown/text/html/json/etc.
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

// resolveAllowedPath expands ~, makes rawPath absolute, resolves symlinks on
// both it and every known root, and only allows it through if it falls
// inside one of those resolved roots. Symlink resolution is required on both
// sides of the comparison: a symlink planted inside an allowed root could
// otherwise point anywhere on disk and slip past a plain prefix check.
func resolveAllowedPath(rawPath string, roots []mcpRoot) (string, error) {
	p := expandTilde(rawPath)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		p = abs
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", rawPath)
	}
	resolved = filepath.Clean(resolved)

	for _, r := range roots {
		if resolved == r.Path || strings.HasPrefix(resolved, r.Path+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path outside indexed workspaces: %s", rawPath)
}

// windowContent slices full (by rune, i.e. by character, matching the
// offset_chars/max_chars naming) starting at offset for up to maxChars
// characters, reporting the total length, whether more remains, and the
// offset a follow-up call should use to continue.
func windowContent(full string, offset, maxChars int) (content string, total int, truncated bool, nextOffset int) {
	runes := []rune(full)
	total = len(runes)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + maxChars
	if end > total {
		end = total
	}
	content = string(runes[offset:end])
	truncated = end < total
	if truncated {
		nextOffset = end
	}
	return content, total, truncated, nextOffset
}

// --- list_workspaces -----------------------------------------------------------

type workspaceInfo struct {
	Name          string  `json:"name"`
	RootPath      string  `json:"root_path"`
	Documents     int     `json:"documents"`
	Chunks        int     `json:"chunks"`
	EmbedCoverage float64 `json:"embed_coverage"`
}

type listWorkspacesInput struct{}

type listWorkspacesOutput struct {
	Workspaces []workspaceInfo `json:"workspaces"`
}

func listWorkspacesHandler(app *AppState) mcp.ToolHandlerFor[listWorkspacesInput, listWorkspacesOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ listWorkspacesInput) (*mcp.CallToolResult, listWorkspacesOutput, error) {
		infos, err := computeWorkspaceInfo(app)
		if err != nil {
			return nil, listWorkspacesOutput{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d workspace(s):\n", len(infos))
		for _, w := range infos {
			fmt.Fprintf(&b, "- %s (%s): %d documents, %d chunks, %.0f%% embedded\n",
				w.Name, w.RootPath, w.Documents, w.Chunks, w.EmbedCoverage*100)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}},
			listWorkspacesOutput{Workspaces: infos}, nil
	}
}

// computeWorkspaceInfo builds counts + embedding coverage per workspace.
// Vector IDs are fetched once (app.Vectors.IDs()) and reused as an in-memory
// set for every workspace's diff, per the design's "build vector ID set once
// per call; fine at current scale" note — avoids an O(workspaces) number of
// full vector-store scans.
func computeWorkspaceInfo(app *AppState) ([]workspaceInfo, error) {
	workspaces, err := app.DB.ListWorkspaces()
	if err != nil {
		return nil, err
	}

	vecIDs := make(map[string]bool, app.Vectors.Count())
	for _, id := range app.Vectors.IDs() {
		vecIDs[id] = true
	}

	out := make([]workspaceInfo, 0, len(workspaces))
	for _, ws := range workspaces {
		docs, err := app.DB.GetDocumentsByWorkspace(ws.ID)
		if err != nil {
			return nil, err
		}
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
		out = append(out, workspaceInfo{
			Name:          ws.Name,
			RootPath:      ws.RootPath,
			Documents:     len(docs),
			Chunks:        len(chunkIDs),
			EmbedCoverage: coverage,
		})
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
	Workspaces    []workspaceInfo `json:"workspaces"`
	Embedder      embedderStatus  `json:"embedder"`
	Embedding     embeddingStatus `json:"embedding"`
	PhaseARunning []string        `json:"phase_a_running"`
	Watching      bool            `json:"watching"`
}

func indexStatusHandler(app *AppState, cfg *config.Config, state *serverState, roots []mcpRoot, worker *index.EmbedWorker) mcp.ToolHandlerFor[indexStatusInput, indexStatusOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ indexStatusInput) (*mcp.CallToolResult, indexStatusOutput, error) {
		infos, err := computeWorkspaceInfo(app)
		if err != nil {
			return nil, indexStatusOutput{}, err
		}

		embed := embedderStatus{Configured: cfg.HasEmbedder()}
		if embed.Configured {
			embed.Model = cfg.Embedder.Model
		}

		out := indexStatusOutput{
			Workspaces:    infos,
			Embedder:      embed,
			Embedding:     embeddingStatusFor(cfg, worker),
			PhaseARunning: state.runningRoots(),
			Watching:      state.isWatching(),
		}

		var b strings.Builder
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
