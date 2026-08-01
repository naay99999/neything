package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
)

// mcpTestEnv bundles everything a test needs to drive the 4 MCP tools
// through an in-process client/server pair (mcp.NewInMemoryTransports —
// see M3.4 of the plan), without touching stdio or the CLI's writer lock.
type mcpTestEnv struct {
	cs    *mcp.ClientSession
	app   *AppState
	state *serverState
	root  string // resolved (symlink-evaluated) corpus root
	// rs is the server's live root set, for tests that need to register a root
	// the way runMCP does internally (see mcp_security_test.go). nil unless the
	// constructor wired it.
	rs *rootSet
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

	app, err := initAppWithOptions(cfg, false)
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
	state := newServerState(rootNames(roots), false)
	server := newMCPServer(mcpDeps{app: app, cfg: cfg, state: state, rs: newRootSet(roots), flt: newPathFilter(cfg)})

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
	app, err := initAppWithOptions(cfg, false)
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
	state := newServerState(rootNames(roots), false)
	state.setPhaseA("corpus", true)

	server := newMCPServer(mcpDeps{app: app, cfg: cfg, state: state, rs: newRootSet(roots), flt: newPathFilter(cfg)})
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

// TestWindowContentMultibyteEquivalence checks windowContent's incremental
// byte-offset decode (added to avoid allocating a full []rune copy of large
// files on every paginated read_document call) against a naive
// []rune-slice reference implementation — the exact algorithm windowContent
// used before that change — across Thai (3-byte) and emoji (4-byte) UTF-8
// content, and offsets that land mid-rune-boundary-adjacent, at the very
// end, and past the end of the content.
func TestWindowContentMultibyteEquivalence(t *testing.T) {
	reference := func(full string, offset, maxChars int) (string, int, bool, int) {
		runes := []rune(full)
		total := len(runes)
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
		content := string(runes[offset:end])
		truncated := end < total
		next := 0
		if truncated {
			next = end
		}
		return content, total, truncated, next
	}

	samples := []string{
		"สวัสดีครับ นี่คือการทดสอบภาษาไทย ทดสอบตัวอักษรหลายไบต์", // Thai, 3-byte runes
		"hello 👋 world 🌍 multibyte emoji test 🚀🚀🚀 done", // emoji, 4-byte runes
		"mixed ภาษาไทย and 🎉 emoji and plain ascii text together",
		"",
		"x",
	}
	offsets := []int{0, 1, 3, 5, 100, -1}
	maxCharsCases := []int{0, 1, 2, 5, 1000}

	for _, s := range samples {
		for _, off := range offsets {
			for _, mc := range maxCharsCases {
				wantContent, wantTotal, wantTrunc, wantNext := reference(s, off, mc)
				gotContent, gotTotal, gotTrunc, gotNext := windowContent(s, off, mc)
				if gotContent != wantContent || gotTotal != wantTotal || gotTrunc != wantTrunc || gotNext != wantNext {
					t.Errorf("windowContent(%q, %d, %d) = (%q, %d, %v, %d), want (%q, %d, %v, %d)",
						s, off, mc, gotContent, gotTotal, gotTrunc, gotNext, wantContent, wantTotal, wantTrunc, wantNext)
				}
			}
		}
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

// --- list_projects + index_status ---------------------------------------------

func TestMCPListProjectsAndIndexStatus(t *testing.T) {
	env := newMCPTestEnv(t)

	projOut := callTool[listProjectsOutput](t, env, "list_projects", listProjectsInput{})
	if len(projOut.Projects) != 1 {
		t.Fatalf("expected exactly 1 project, got %d: %+v", len(projOut.Projects), projOut.Projects)
	}
	p := projOut.Projects[0]
	if p.Name != "corpus" {
		t.Fatalf("expected project name=corpus, got %q", p.Name)
	}
	if !p.Indexed {
		t.Fatal("expected corpus to be indexed (it's a served root)")
	}
	if p.Documents != 2 {
		t.Fatalf("expected 2 documents (notes.md, sub/more.md), got %d", p.Documents)
	}
	if p.Chunks == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	statusOut := callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	if len(statusOut.Workspaces) != 1 || statusOut.Workspaces[0].Name != "corpus" {
		t.Fatalf("expected index_status to report the corpus workspace, got %+v", statusOut.Workspaces)
	}
	if statusOut.Workspaces[0].Chunks != p.Chunks || statusOut.Workspaces[0].Documents != p.Documents {
		t.Fatalf("index_status counts should match list_projects: %+v vs %+v", statusOut.Workspaces[0], p)
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

// TestMCPListWorkspacesToolRemoved guards the tool-surface rewire: the
// list_workspaces tool no longer appears in tools/list, and list_projects
// takes its place.
func TestMCPListWorkspacesToolRemoved(t *testing.T) {
	env := newMCPTestEnv(t)
	res, err := env.cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tl := range res.Tools {
		names[tl.Name] = true
	}
	if names["list_workspaces"] {
		t.Fatal("expected list_workspaces to be removed from the tool surface")
	}
	if !names["list_projects"] {
		t.Fatal("expected list_projects to be registered")
	}
	for _, want := range []string{"get_context", "remember", "update_profile"} {
		if !names[want] {
			t.Fatalf("expected %s to be registered", want)
		}
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
	_ = callTool[listProjectsOutput](t, env, "list_projects", listProjectsInput{})
	_ = callTool[getContextOutput](t, env, "get_context", getContextInput{})
	_ = callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	_ = callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: filepath.Join(env.root, "notes.md")})

	restore()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if n > 0 {
		t.Fatalf("expected zero bytes on stdout, got %d: %q", n, buf[:n])
	}
}

// --- read_document: secret-file deny --------------------------------------------

func TestMCPReadDocumentDeniesSecretFiles(t *testing.T) {
	env := newMCPTestEnv(t)

	denied := []string{
		filepath.Join(env.root, ".env"),
		filepath.Join(env.root, "id_rsa"),
		filepath.Join(env.root, "sub", "passwords.md"),
		filepath.Join(env.root, ".ssh", "id_rsa"),
	}
	for _, p := range denied {
		writeTestFile(t, p, "SECRET=verysecret")
		msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: p})
		if !strings.Contains(msg, "excluded by security policy") {
			t.Errorf("expected security-policy rejection for %s, got: %s", p, msg)
		}
	}

	// A normal file under the same root still reads fine.
	out := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: filepath.Join(env.root, "notes.md")})
	if !strings.Contains(out.Content, "order-1233") {
		t.Fatalf("normal file should still be readable, got: %q", out.Content)
	}
}

// --- read-only mode -------------------------------------------------------------

func TestMCPReadOnlyModeIndexStatus(t *testing.T) {
	env := newMCPTestEnvReadOnly(t)
	out := callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	if out.Mode != "read-only" {
		t.Fatalf("expected mode=read-only, got %q", out.Mode)
	}
}

func TestMCPReadWriteModeIndexStatus(t *testing.T) {
	env := newMCPTestEnv(t)
	out := callTool[indexStatusOutput](t, env, "index_status", indexStatusInput{})
	if out.Mode != "read-write" {
		t.Fatalf("expected mode=read-write, got %q", out.Mode)
	}
}

// newMCPTestEnvReadOnly mirrors newMCPTestEnv but constructs the server the
// way runMCP does when the writer lock was held: readOnly server state, no
// indexer, no worker.
func newMCPTestEnvReadOnly(t *testing.T) *mcpTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.md"), "read-only corpus note\n")
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	app, err := initAppWithOptions(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.DB.Close()
		app.Vectors.Close()
	})

	roots := []mcpRoot{{Name: "corpus", Path: resolvedRoot}}
	state := newServerState(rootNames(roots), true)
	server := newMCPServer(mcpDeps{app: app, cfg: cfg, state: state, rs: newRootSet(roots), flt: newPathFilter(cfg)})

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

// --- search_folder: user-directed whole-home fallback ----------------------------

func TestMCPSearchFolderFindsAndAllowsRead(t *testing.T) {
	env := newMCPTestEnv(t)

	// A folder OUTSIDE the served root but under $HOME (HOME is a temp dir in
	// this env), simulating ~/Downloads.
	home, _ := os.UserHomeDir()
	downloads := filepath.Join(home, "Downloads")
	target := filepath.Join(downloads, "thaibulk-submission.md")
	writeTestFile(t, target, "thaibulk submission steps: register, verify sender, submit.\n")
	writeTestFile(t, filepath.Join(downloads, "passwords.md"), "thaibulk password hunter2\n")

	// Before discovery: read must be denied (outside every root).
	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: target})
	if !strings.Contains(msg, "path outside indexed workspaces") {
		t.Fatalf("expected outside-workspace rejection before discovery, got: %s", msg)
	}

	// User says "look in Downloads" -> search_folder.
	out := callTool[searchFolderOutput](t, env, "search_folder", searchFolderInput{Path: downloads, Query: "thaibulk"})
	if len(out.Results) != 1 {
		t.Fatalf("expected exactly 1 hit (secret passwords.md must be hidden), got: %+v", out.Results)
	}
	if filepath.Base(out.Results[0].Path) != "thaibulk-submission.md" {
		t.Fatalf("expected thaibulk-submission.md, got %s", out.Results[0].Path)
	}

	// After discovery: the surfaced file is readable.
	read := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: target})
	if !strings.Contains(read.Content, "verify sender") {
		t.Fatalf("expected discovered file to be readable, got: %q", read.Content)
	}

	// But a sibling that was NOT surfaced still isn't.
	other := filepath.Join(downloads, "unrelated.md")
	writeTestFile(t, other, "nothing to do with the query")
	msg = callToolExpectError(t, env, "read_document", readDocumentInput{Path: other})
	if !strings.Contains(msg, "path outside indexed workspaces") {
		t.Fatalf("expected undiscovered sibling to stay unreadable, got: %s", msg)
	}
}

func TestMCPSearchFolderRejectsOutsideHome(t *testing.T) {
	env := newMCPTestEnv(t)
	msg := callToolExpectError(t, env, "search_folder", searchFolderInput{Path: "/etc", Query: "passwd"})
	if !strings.Contains(msg, "outside the home directory") {
		t.Fatalf("expected outside-home rejection for /etc, got: %s", msg)
	}
}

func TestMCPSearchFolderNeverSurfacesSecrets(t *testing.T) {
	env := newMCPTestEnv(t)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "stuff")
	writeTestFile(t, filepath.Join(dir, ".env"), "TOKEN=widget\n")
	writeTestFile(t, filepath.Join(dir, "widget-credentials.json"), `{"widget":1}`)
	writeTestFile(t, filepath.Join(dir, ".ssh", "id_rsa"), "widget key\n")

	out := callTool[searchFolderOutput](t, env, "search_folder", searchFolderInput{Path: dir, Query: "widget"})
	if len(out.Results) != 0 {
		t.Fatalf("secret files must never be surfaced, got: %+v", out.Results)
	}
}

// --- index_folder ----------------------------------------------------------------

// newMCPTestEnvIndexable builds an env whose deps include a live indexer +
// serialize, as runMCP wires in read-write mode — required by index_folder.
func newMCPTestEnvIndexable(t *testing.T) *mcpTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	app, err := initAppWithOptions(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.DB.Close()
		app.Vectors.Close()
	})

	ix, err := newIndexer(app, cfg)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	state := newServerState(nil, false)
	server := newMCPServer(mcpDeps{
		app: app, cfg: cfg, state: state, rs: newRootSet(nil),
		flt: newPathFilter(cfg), ix: ix,
		serialize: func(run func()) { mu.Lock(); defer mu.Unlock(); run() },
	})

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

	home, _ := os.UserHomeDir()
	return &mcpTestEnv{cs: cs, app: app, state: state, root: home}
}

func TestMCPIndexFolderMakesFolderSearchableAndReadable(t *testing.T) {
	env := newMCPTestEnvIndexable(t)

	home, _ := os.UserHomeDir()
	folder := filepath.Join(home, "reports")
	target := filepath.Join(folder, "q3-summary.md")
	writeTestFile(t, target, "Quarterly zebra-metrics grew 42 percent.\n")
	writeTestFile(t, filepath.Join(folder, "secrets.md"), "zebra password\n")

	out := callTool[indexFolderOutput](t, env, "index_folder", indexFolderInput{Path: folder})
	if out.Workspace != "reports" || out.FilesScanned != 1 || out.Chunks == 0 {
		t.Fatalf("unexpected index result: %+v", out)
	}

	// Content searchable via the normal index path.
	res := callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: "zebra-metrics"})
	found := false
	for _, r := range res.Results {
		if strings.Contains(r.Path, "q3-summary.md") && r.Source == "index" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected indexed hit for q3-summary.md, got %+v", res.Results)
	}

	// Readable immediately — the dynamic root set admitted the new root.
	read := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: target})
	if !strings.Contains(read.Content, "42 percent") {
		t.Fatalf("expected file content, got %q", read.Content)
	}

	// Secret file under the new root: indexed never, read denied.
	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: filepath.Join(folder, "secrets.md")})
	if !strings.Contains(msg, "excluded by security policy") {
		t.Fatalf("expected security rejection, got: %s", msg)
	}

	// Idempotent re-index: second call succeeds with skips, no error.
	again := callTool[indexFolderOutput](t, env, "index_folder", indexFolderInput{Path: folder})
	if again.Workspace != "reports" {
		t.Fatalf("re-index should reuse the workspace, got %+v", again)
	}
}

func TestMCPIndexFolderRejectsReadOnly(t *testing.T) {
	env := newMCPTestEnvReadOnly(t)
	home, _ := os.UserHomeDir()
	msg := callToolExpectError(t, env, "index_folder", indexFolderInput{Path: home})
	if !strings.Contains(msg, "read-only") {
		t.Fatalf("expected read-only rejection, got: %s", msg)
	}
}

func TestMCPIndexFolderRejectsOutsideHome(t *testing.T) {
	env := newMCPTestEnvIndexable(t)
	msg := callToolExpectError(t, env, "index_folder", indexFolderInput{Path: "/etc"})
	if !strings.Contains(msg, "outside the home directory") {
		t.Fatalf("expected outside-home rejection, got: %s", msg)
	}
}

func TestMCPIndexFolderRejectsNameCollision(t *testing.T) {
	env := newMCPTestEnvIndexable(t)
	home, _ := os.UserHomeDir()
	folder := filepath.Join(home, "sub", "notes")
	writeTestFile(t, filepath.Join(folder, "a.md"), "hello\n")
	// Same basename already bound to a different path.
	if _, err := env.app.DB.UpsertWorkspace("notes", "/somewhere/else/notes"); err != nil {
		t.Fatal(err)
	}
	msg := callToolExpectError(t, env, "index_folder", indexFolderInput{Path: folder})
	if !strings.Contains(msg, "already bound") {
		t.Fatalf("expected collision rejection, got: %s", msg)
	}
}

// --- layered context: get_context / list_projects / remember / update_profile ----

// requireGitForMCPTest skips the test if git isn't on PATH — ScanRepos
// shells out to git, matching internal/context's own test guard.
func requireGitForMCPTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// initGitFixtureRepo creates a one-commit git repo at dir, for tests that
// exercise ScanRepos indirectly through get_context/list_projects.
func initGitFixtureRepo(t *testing.T, dir, subject string) {
	t.Helper()
	requireGitForMCPTest(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	writeTestFile(t, filepath.Join(dir, "README.md"), "# hi\n")
	run("add", "README.md")
	run("commit", "-q", "-m", subject)
}

// newMCPTestEnvFullLoop builds a write-mode server the way runMCP does when
// serving a dev root (found via cfg.Context.DevRoots' default ~/workspace)
// plus the always-on memory workspace, so a test can drive the whole
// get_context -> remember -> search_documents loop through the real tool
// handlers. Returns the env plus the indexer, so a test can trigger a
// re-index of the memory workspace itself instead of standing up a real
// fsnotify watcher (equivalent to "wait for Phase A" per the plan).
func newMCPTestEnvFullLoop(t *testing.T) (*mcpTestEnv, *index.Indexer) {
	t.Helper()
	requireGitForMCPTest(t)
	t.Setenv("HOME", t.TempDir())

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(home, "workspace", "myproject")
	initGitFixtureRepo(t, repoDir, "initial commit")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Context.DevRoots) != 1 {
		t.Fatalf("expected dev_roots to default to the freshly-created ~/workspace, got %v", cfg.Context.DevRoots)
	}

	app, err := initAppWithOptions(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.DB.Close()
		app.Vectors.Close()
	})

	memPath := memoryDir()
	if err := os.MkdirAll(memPath, 0700); err != nil {
		t.Fatal(err)
	}

	ix, err := newIndexer(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), memPath, "memory"); err != nil {
		t.Fatal(err)
	}

	// Internal: true mirrors runMCP — ~/.ney/memory lives under a dot-dir on
	// purpose, and that bit is what exempts it from the from-$HOME deny.
	roots := []mcpRoot{{Name: "memory", Path: memPath, Internal: true}}
	state := newServerState(rootNames(roots), false)
	var mu sync.Mutex
	server := newMCPServer(mcpDeps{
		app: app, cfg: cfg, state: state, rs: newRootSet(roots),
		flt: newPathFilter(cfg), ix: ix,
		serialize: func(run func()) { mu.Lock(); defer mu.Unlock(); run() },
	})

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

	return &mcpTestEnv{cs: cs, app: app, state: state, root: memPath}, ix
}

func TestMCPFullContextLoop(t *testing.T) {
	env, ix := newMCPTestEnvFullLoop(t)

	// get_context: profile.md doesn't exist yet, so LoadProfile creates it
	// from the template — its section headings must show up in the render —
	// plus the fixture repo under the dev root.
	ctxOut := callTool[getContextOutput](t, env, "get_context", getContextInput{})
	if !strings.Contains(ctxOut.Context, "Name & role") {
		t.Fatalf("expected the profile template marker in get_context output, got: %s", ctxOut.Context)
	}
	if !strings.Contains(ctxOut.Context, "myproject") {
		t.Fatalf("expected the fixture repo name in get_context output, got: %s", ctxOut.Context)
	}

	// remember: writes a new memory file under ~/.ney/memory.
	remOut := callTool[rememberOutput](t, env, "remember", rememberInput{
		Title:   "ney pivot decision",
		Content: "Decided to reposition ney as a personal context server; zebraquokka marker.",
	})
	if remOut.Path == "" {
		t.Fatal("expected a non-empty path from remember")
	}
	if _, err := os.Stat(remOut.Path); err != nil {
		t.Fatalf("expected the memory file to exist on disk: %v", err)
	}

	// Simulate the memory workspace's watcher picking up the new file (the
	// real server has one running continuously; here we just re-run Phase A
	// once, deterministically, instead of waiting on fsnotify).
	if _, err := ix.Index(context.Background(), env.root, "memory"); err != nil {
		t.Fatal(err)
	}

	searchOut := callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: "zebraquokka marker"})
	found := false
	for _, r := range searchOut.Results {
		if r.Path == remOut.Path {
			found = true
			if r.Source != "index" {
				t.Fatalf("expected source=index for the newly-indexed memory, got %q", r.Source)
			}
		}
	}
	if !found {
		t.Fatalf("expected the new memory to be searchable after re-indexing, got %+v", searchOut.Results)
	}
}

func TestMCPRememberWorksOnReadOnlyServer(t *testing.T) {
	env := newMCPTestEnvReadOnly(t)

	out := callTool[rememberOutput](t, env, "remember", rememberInput{
		Title:   "read-only remember test",
		Content: "written while the server is read-only",
	})
	if out.Path == "" {
		t.Fatal("expected a non-empty path")
	}
	data, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatalf("expected the memory file to exist at %s: %v", out.Path, err)
	}
	if !strings.Contains(string(data), "written while the server is read-only") {
		t.Fatalf("expected the written file to contain the memory content, got: %s", data)
	}
	wantDir := filepath.Join(config.NeyDir(), "memory")
	if filepath.Dir(out.Path) != wantDir {
		t.Fatalf("expected the memory to be written under %s, got %s", wantDir, out.Path)
	}
}

func TestMCPRememberRequiresTitleAndContent(t *testing.T) {
	env := newMCPTestEnv(t)
	if msg := callToolExpectError(t, env, "remember", rememberInput{Content: "no title"}); !strings.Contains(msg, "title is required") {
		t.Fatalf("expected 'title is required', got: %s", msg)
	}
	if msg := callToolExpectError(t, env, "remember", rememberInput{Title: "no content"}); !strings.Contains(msg, "content is required") {
		t.Fatalf("expected 'content is required', got: %s", msg)
	}
}

func TestMCPUpdateProfileRoundTrip(t *testing.T) {
	env := newMCPTestEnv(t)

	upOut := callTool[updateProfileOutput](t, env, "update_profile", updateProfileInput{
		Section: "Current focus",
		Content: "Building the layered context MCP surface.",
	})
	if upOut.Path == "" {
		t.Fatal("expected a non-empty path")
	}

	ctxOut := callTool[getContextOutput](t, env, "get_context", getContextInput{})
	if !strings.Contains(ctxOut.Context, "Building the layered context MCP surface.") {
		t.Fatalf("expected get_context to reflect the profile update, got: %s", ctxOut.Context)
	}

	// Works read-only too, same reasoning as remember.
	roEnv := newMCPTestEnvReadOnly(t)
	roOut := callTool[updateProfileOutput](t, roEnv, "update_profile", updateProfileInput{
		Section: "Working style",
		Content: "Prefers terse commit messages.",
	})
	data, err := os.ReadFile(roOut.Path)
	if err != nil {
		t.Fatalf("expected profile.md to exist: %v", err)
	}
	if !strings.Contains(string(data), "Prefers terse commit messages.") {
		t.Fatalf("expected profile.md to contain the update, got: %s", data)
	}
}
