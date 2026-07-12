package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/store"
)

// mcpTestEnv bundles everything a test needs to drive the 4 MCP tools
// through an in-process client/server pair (mcp.NewInMemoryTransports —
// see M3.4 of the plan), without touching stdio or the CLI's writer lock.
type mcpTestEnv struct {
	cs    *mcp.ClientSession
	app   *AppState
	state *serverState
	root  string // resolved (symlink-evaluated) corpus root
}

// newMCPTestEnv sets up an isolated ~/.ney (temp HOME), indexes a small
// corpus via Phase A only (no embedder — keyword/FTS-only, matching the
// "search before/without an embedder" scenario the plan calls out), and
// connects an in-memory MCP client to a server built with newMCPServer (the
// same factory `ney mcp` uses).
func newMCPTestEnv(t *testing.T) *mcpTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.md"), "# Billing\n\nInvoice order-1233 was paid by Alice for 4200 baht.\n")
	writeTestFile(t, filepath.Join(root, "sub", "more.md"), "Widgets and gadgets galore, quarterly revenue notes.\n")

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	app, err := initAppWithOptions(cfg, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.DB.Close()
		app.Vectors.Close()
	})

	if _, err := app.DB.UpsertWorkspace("corpus", resolvedRoot); err != nil {
		t.Fatal(err)
	}

	ix, err := newIndexer(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), resolvedRoot, "corpus"); err != nil {
		t.Fatal(err)
	}

	roots := []mcpRoot{{Name: "corpus", Path: resolvedRoot}}
	state := newServerState(rootNames(roots))
	server := newMCPServer(app, cfg, state, roots, nil)

	t1, t2 := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	return &mcpTestEnv{cs: cs, app: app, state: state, root: resolvedRoot}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// callTool calls an MCP tool and decodes its structured output into T,
// failing the test on a transport error or a tool-level error result.
func callTool[T any](t *testing.T, env *mcpTestEnv, name string, args any) T {
	t.Helper()
	var zero T
	res, err := env.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tool %s returned an error result: %s", name, toolResultText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content for %s: %v", name, err)
	}
	out := zero
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal structured content for %s into %T: %v (raw: %s)", name, out, err, b)
	}
	return out
}

// callToolExpectError calls a tool expecting it to fail (either a
// protocol-level error, e.g. bad input, or a tool-level IsError result, e.g.
// a rejected path) and returns a combined error message.
func callToolExpectError(t *testing.T, env *mcpTestEnv, name string, args any) string {
	t.Helper()
	res, err := env.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if res.IsError {
		return toolResultText(res)
	}
	t.Fatalf("tool %s unexpectedly succeeded", name)
	return ""
}

func toolResultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// --- search_documents ---------------------------------------------------------

func TestMCPSearchDocumentsKeywordOnly(t *testing.T) {
	env := newMCPTestEnv(t)

	out := callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: "invoice order-1233"})

	if len(out.Results) == 0 {
		t.Fatal("expected at least one FTS result for 'invoice order-1233'")
	}
	if !out.Meta.KeywordUsed {
		t.Fatal("expected meta.keyword_used=true")
	}
	if out.Meta.SemanticUsed {
		t.Fatal("expected meta.semantic_used=false — no embedder is configured in this env")
	}
	if out.Meta.Degraded == "" {
		t.Fatal("expected a degraded note when no embedder is configured")
	}
	found := false
	for _, r := range out.Results {
		if strings.Contains(r.Path, "notes.md") {
			found = true
			if r.Source != "index" {
				t.Fatalf("expected source=index, got %q", r.Source)
			}
			if r.Workspace != "corpus" {
				t.Fatalf("expected workspace=corpus, got %q", r.Workspace)
			}
		}
	}
	if !found {
		t.Fatalf("expected notes.md among results, got %+v", out.Results)
	}
	if out.IndexStatus.EmbeddingState != "disabled" {
		t.Fatalf("expected embedding_state=disabled with no embedder configured, got %q", out.IndexStatus.EmbeddingState)
	}
}

func TestMCPSearchDocumentsRequiresQuery(t *testing.T) {
	env := newMCPTestEnv(t)
	msg := callToolExpectError(t, env, "search_documents", searchDocumentsInput{Query: "  "})
	if !strings.Contains(msg, "query is required") {
		t.Fatalf("expected 'query is required' error, got: %s", msg)
	}
}

// TestMCPSearchDocumentsLiveScanDuringPhaseA covers the M4 tier-0 wiring
// (design §6.1): while a root's Phase A scan is still marked running in
// serverState, search_documents supplements index results with a live
// filesystem scan of that root, so a file that exists on disk but has no
// chunk/FTS rows yet still shows up — tagged source "live-scan" — instead
// of the query coming back empty.
func TestMCPSearchDocumentsLiveScanDuringPhaseA(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "order-9999-receipt.md"), "Receipt for order-9999, paid by Bob.\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	app, err := initAppWithOptions(cfg, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.DB.Close()
		app.Vectors.Close()
	})
	if _, err := app.DB.UpsertWorkspace("corpus", resolvedRoot); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip ix.Index — nothing is indexed yet, simulating a
	// query that arrives while Phase A (marked below) is still in flight.

	roots := []mcpRoot{{Name: "corpus", Path: resolvedRoot}}
	state := newServerState(rootNames(roots))
	state.setPhaseA("corpus", true)

	server := newMCPServer(app, cfg, state, roots, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	env := &mcpTestEnv{cs: cs, app: app, state: state, root: resolvedRoot}
	out := callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: "order-9999"})

	if !out.IndexStatus.PhaseARunning {
		t.Fatal("expected index_status.phase_a_running=true while Phase A is marked running")
	}
	found := false
	for _, r := range out.Results {
		if strings.Contains(r.Path, "order-9999-receipt.md") {
			found = true
			if r.Source != "live-scan" {
				t.Fatalf("expected source=live-scan for an unindexed hit found via scan, got %q", r.Source)
			}
		}
	}
	if !found {
		t.Fatalf("expected a live-scan hit for order-9999-receipt.md, got %+v", out.Results)
	}
}

// --- read_document: plain text + windowing -------------------------------------

func TestMCPReadDocumentPlainTextWindowing(t *testing.T) {
	env := newMCPTestEnv(t)
	fullPath := filepath.Join(env.root, "notes.md")

	full := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: fullPath})
	if full.Source != "file" {
		t.Fatalf("expected source=file, got %q", full.Source)
	}
	if full.Truncated {
		t.Fatal("expected the whole (small) file to fit without truncation")
	}
	if !strings.Contains(full.Content, "order-1233") {
		t.Fatalf("expected file content to include order-1233, got: %q", full.Content)
	}

	windowed := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: fullPath, OffsetChars: 0, MaxChars: 5})
	if len(windowed.Content) == 0 || len([]rune(windowed.Content)) != 5 {
		t.Fatalf("expected exactly 5 characters, got %q", windowed.Content)
	}
	if !windowed.Truncated {
		t.Fatal("expected truncated=true when max_chars cuts off content")
	}
	if windowed.NextOffset != 5 {
		t.Fatalf("expected next_offset=5, got %d", windowed.NextOffset)
	}
	if windowed.TotalChars != full.TotalChars {
		t.Fatalf("total_chars should be stable across windowed calls: got %d want %d", windowed.TotalChars, full.TotalChars)
	}

	// Follow-up call using next_offset should continue seamlessly.
	rest := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: fullPath, OffsetChars: windowed.NextOffset})
	if windowed.Content+rest.Content != full.Content {
		t.Fatalf("windowed reads did not reassemble to the full content:\nwant: %q\ngot:  %q", full.Content, windowed.Content+rest.Content)
	}
}

// --- read_document: path guard --------------------------------------------------

func TestMCPReadDocumentPathGuardRejectsOutsidePath(t *testing.T) {
	env := newMCPTestEnv(t)

	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "secret.md")
	writeTestFile(t, outsidePath, "top secret, not in any indexed workspace")

	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: outsidePath})
	if !strings.Contains(msg, "path outside indexed workspaces") {
		t.Fatalf("expected 'path outside indexed workspaces' error, got: %s", msg)
	}
}

func TestMCPReadDocumentPathGuardRejectsSymlinkEscape(t *testing.T) {
	env := newMCPTestEnv(t)

	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.md")
	writeTestFile(t, secretPath, "top secret, reached via a symlink planted inside the root")

	linkPath := filepath.Join(env.root, "escape-link.md")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Fatal(err)
	}

	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: linkPath})
	if !strings.Contains(msg, "path outside indexed workspaces") {
		t.Fatalf("expected 'path outside indexed workspaces' error for a symlink escaping the root, got: %s", msg)
	}
}

// --- read_document: binary (pdf/docx) chunk reassembly --------------------------

func TestMCPReadDocumentBinaryChunkReassembly(t *testing.T) {
	env := newMCPTestEnv(t)

	// A real PDF/DOCX loader round-trip is out of scope here — what
	// read_document actually needs to prove is the chunk-reassembly path
	// itself (join by chunk_index), so seed a document+chunk rows directly,
	// same as the indexer would have produced. The path guard requires the
	// file to exist on disk (EvalSymlinks), so create a dummy one too — its
	// bytes are never read on this path.
	pdfPath := filepath.Join(env.root, "report.pdf")
	writeTestFile(t, pdfPath, "%PDF-1.4 dummy bytes, never read via the chunks path\n")

	ws, err := env.app.DB.GetWorkspaceByName("corpus")
	if err != nil || ws == nil {
		t.Fatalf("expected corpus workspace to exist: %v", err)
	}
	docID, err := env.app.DB.UpsertDocument(&store.Document{
		WorkspaceID: ws.ID,
		Path:        pdfPath,
		Type:        "pdf",
		Hash:        "deadbeef",
		SizeBytes:   42,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.app.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	chunks := []*store.Chunk{
		{DocumentID: docID, ChunkIndex: 0, Content: "Part one of the report.", StartPos: 1, EndPos: 1},
		{DocumentID: docID, ChunkIndex: 1, Content: "Part two of the report.", StartPos: 2, EndPos: 2},
	}
	if err := env.app.DB.InsertChunks(tx, chunks); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	out := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: pdfPath})
	if out.Source != "chunks" {
		t.Fatalf("expected source=chunks for an indexed pdf, got %q", out.Source)
	}
	want := "Part one of the report.\nPart two of the report."
	if out.Content != want {
		t.Fatalf("expected chunk-joined content %q, got %q", want, out.Content)
	}
}

// --- list_workspaces + index_status ---------------------------------------------

func TestMCPListWorkspacesAndIndexStatus(t *testing.T) {
	env := newMCPTestEnv(t)

	wsOut := callTool[listWorkspacesOutput](t, env, "list_workspaces", listWorkspacesInput{})
	if len(wsOut.Workspaces) != 1 {
		t.Fatalf("expected exactly 1 workspace, got %d: %+v", len(wsOut.Workspaces), wsOut.Workspaces)
	}
	w := wsOut.Workspaces[0]
	if w.Name != "corpus" {
		t.Fatalf("expected workspace name=corpus, got %q", w.Name)
	}
	if w.Documents != 2 {
		t.Fatalf("expected 2 documents (notes.md, sub/more.md), got %d", w.Documents)
	}
	if w.Chunks == 0 {
		t.Fatal("expected at least 1 chunk")
	}
	if w.EmbedCoverage != 0 {
		t.Fatalf("expected embed_coverage=0 with no embedder configured, got %v", w.EmbedCoverage)
	}

	statusOut := callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	if len(statusOut.Workspaces) != 1 || statusOut.Workspaces[0].Name != "corpus" {
		t.Fatalf("expected index_status to report the corpus workspace, got %+v", statusOut.Workspaces)
	}
	if statusOut.Workspaces[0].Chunks != w.Chunks || statusOut.Workspaces[0].Documents != w.Documents {
		t.Fatalf("index_status counts should match list_workspaces: %+v vs %+v", statusOut.Workspaces[0], w)
	}
	if statusOut.Embedder.Configured {
		t.Fatal("expected embedder.configured=false — this test env has no embedder")
	}
	if statusOut.Embedding.State != "disabled" {
		t.Fatalf("expected embedding.state=disabled, got %q", statusOut.Embedding.State)
	}
	if len(statusOut.PhaseARunning) != 0 {
		t.Fatalf("expected no roots mid-scan after setup finished indexing synchronously, got %v", statusOut.PhaseARunning)
	}
}

// --- stdout hygiene ---------------------------------------------------------------

// TestMCPStdoutHygiene guards the single most important property of `ney
// mcp`: stdout is reserved for the MCP protocol end to end. It redirects
// os.Stdout for the whole of server setup (config load, app init, Phase A
// indexing, server/tool registration) plus a round of tool calls, and
// asserts not one byte landed there — anything printed to stdout here would
// corrupt the wire protocol for a real stdio-connected client.
func TestMCPStdoutHygiene(t *testing.T) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		os.Stdout = origStdout
		w.Close()
	}
	defer restore()

	env := newMCPTestEnv(t)
	_ = callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: "invoice"})
	_ = callTool[listWorkspacesOutput](t, env, "list_workspaces", listWorkspacesInput{})
	_ = callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	_ = callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: filepath.Join(env.root, "notes.md")})

	restore()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if n > 0 {
		t.Fatalf("expected zero bytes on stdout, got %d: %q", n, buf[:n])
	}
}
