package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/pathfilter"
)

// newMCPSecurityEnv builds a read-write server (indexer + serialize wired, as
// runMCP does) over a temp $HOME and hands back the indexer too, so a test
// can create workspace state the tools themselves now refuse to create — the
// "this workspace predates the fix" scenario.
func newMCPSecurityEnv(t *testing.T) (*mcpTestEnv, *index.Indexer) {
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
	rs := newRootSet(nil)
	server := newMCPServer(mcpDeps{
		app: app, cfg: cfg, state: state, rs: rs,
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

	home, err := resolvedHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return &mcpTestEnv{cs: cs, app: app, state: state, rs: rs, root: home}, ix
}

// --- H-1: a dot-directory must never become a served root -----------------------

// TestMCPIndexFolderRejectsDotDirectory is the regression test for the
// verified H-1 escalation: index_folder(~/.config) used to succeed (only
// "under $HOME" was checked), registering .config as a permanent root — and
// because Filter.ExcludedPath never examines the root component itself, every
// later read under that root skipped the dotfile rule entirely. The secret
// below was readable in full before this fix.
func TestMCPIndexFolderRejectsDotDirectory(t *testing.T) {
	env, _ := newMCPSecurityEnv(t)
	home := env.root

	secret := filepath.Join(home, ".config", "gh", "hosts.md")
	writeTestFile(t, secret, "oauth_token: gho_SUPERSECRET\n")

	for _, target := range []string{
		filepath.Join(home, ".config"),       // the dot-dir itself
		filepath.Join(home, ".config", "gh"), // a normal-looking dir *under* one
		filepath.Join(home, ".ssh"),
	} {
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		msg := callToolExpectError(t, env, "index_folder", indexFolderInput{Path: target})
		if !strings.Contains(msg, "excluded by security policy") {
			t.Errorf("index_folder(%s) should be rejected by the security policy, got: %s", target, msg)
		}
	}

	// ...and with no root admitted, the secret stays unreachable.
	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: secret})
	if strings.Contains(msg, "gho_SUPERSECRET") {
		t.Fatalf("read_document leaked the secret: %s", msg)
	}
	if !strings.Contains(msg, "path outside indexed workspaces") {
		t.Fatalf("expected containment rejection for the secret, got: %s", msg)
	}

	// A normal folder under $HOME still indexes — the guard is about denied
	// components, not about tightening index_folder generally.
	ok := filepath.Join(home, "reports")
	writeTestFile(t, filepath.Join(ok, "q3.md"), "quarterly zebra notes\n")
	out := callTool[indexFolderOutput](t, env, "index_folder", indexFolderInput{Path: ok})
	if out.Workspace != "reports" {
		t.Fatalf("expected the normal folder to still index, got %+v", out)
	}
}

// TestMCPIndexFolderRejectsHome covers L-4: $HOME passes the containment
// check by definition, but indexing it whole is never what the user meant.
func TestMCPIndexFolderRejectsHome(t *testing.T) {
	env, _ := newMCPSecurityEnv(t)
	msg := callToolExpectError(t, env, "index_folder", indexFolderInput{Path: env.root})
	if !strings.Contains(msg, "entire home directory") {
		t.Fatalf("expected the whole-home rejection, got: %s", msg)
	}
}

// --- H-2: search_folder must not grep inside dot-directories --------------------

// TestMCPSearchFolderRejectsDotDirectory is the regression test for the
// verified H-2 leak: search_folder(~/.docker) content-grepped the folder and
// returned the credential blob in a snippet, because internal/scan exempts
// the scan root itself from ExcludedDir (correct for normal roots, a hole
// when the root IS a dot-dir).
func TestMCPSearchFolderRejectsDotDirectory(t *testing.T) {
	env, _ := newMCPSecurityEnv(t)
	home := env.root

	const marker = "aGFja2VyOnAtYS1z-c3dvcmQ="
	writeTestFile(t, filepath.Join(home, ".docker", "config.md"),
		`{"auths": {"registry.io": {"auth": "`+marker+`"}}}`+"\n")
	writeTestFile(t, filepath.Join(home, ".aws", "notes", "creds.md"),
		"aws auths registry "+marker+"\n")

	for _, target := range []string{
		filepath.Join(home, ".docker"),
		filepath.Join(home, ".aws", "notes"), // normal name, denied ancestor
	} {
		msg := callToolExpectError(t, env, "search_folder", searchFolderInput{Path: target, Query: "auths registry"})
		if !strings.Contains(msg, "excluded by security policy") {
			t.Errorf("search_folder(%s) should be rejected, got: %s", target, msg)
		}
		if strings.Contains(msg, marker) {
			t.Errorf("search_folder(%s) leaked secret material: %s", target, msg)
		}
	}

	// Scanning $HOME itself stays allowed (bounded, session-scoped) — and must
	// not walk into the dot-dirs either.
	out := callTool[searchFolderOutput](t, env, "search_folder", searchFolderInput{Path: home, Query: "auths registry"})
	for _, r := range out.Results {
		t.Errorf("scanning $HOME must not surface dot-dir content, got %s: %s", r.Path, r.Snippet)
	}
}

// --- read-time defense in depth -------------------------------------------------

// TestMCPReadDocumentDeniesDotDirRootFromHome covers the case where a denied
// root got into the served set some other way than index_folder — an older
// build, or `ney index ~/.ssh` from the CLI. read_document evaluates the deny
// from $HOME down, so the root's own components are re-examined.
func TestMCPReadDocumentDeniesDotDirRootFromHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, err := resolvedHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	dotRoot := filepath.Join(home, ".config")
	secret := filepath.Join(dotRoot, "gh", "hosts.md")
	writeTestFile(t, secret, "oauth_token: gho_SUPERSECRET\n")

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

	// Served root as a pre-fix install would have persisted it.
	roots := []mcpRoot{{Name: "config", Path: dotRoot}}
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

	env := &mcpTestEnv{cs: cs, app: app, state: state, root: dotRoot}
	msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: secret})
	if !strings.Contains(msg, "excluded by security policy") {
		t.Fatalf("expected the from-$HOME deny to catch a dot-dir root, got: %s", msg)
	}
	if strings.Contains(msg, "gho_SUPERSECRET") {
		t.Fatalf("leaked the secret in the error: %s", msg)
	}
}

// TestMCPReadDocumentAllowsMemoryRoot is the counterweight to the test above,
// and the reason mcpRoot.Internal exists: ~/.ney/memory is a legitimately
// served root that lives under a dot-directory. Evaluating it from $HOME would
// deny every file `remember` ever wrote.
func TestMCPReadDocumentAllowsMemoryRoot(t *testing.T) {
	env, ix := newMCPSecurityEnv(t)

	memPath := memoryDir()
	if err := os.MkdirAll(memPath, 0o700); err != nil {
		t.Fatal(err)
	}
	memPath = resolveRootBestEffort(memPath) // only meaningful once it exists

	rem := callTool[rememberOutput](t, env, "remember", rememberInput{
		Title:   "memory readability",
		Content: "quokkazebra marker for the memory-root read test",
	})
	// Stand in for the memory workspace's watcher (see TestMCPFullContextLoop).
	if _, err := ix.Index(context.Background(), memPath, "memory"); err != nil {
		t.Fatal(err)
	}

	// The server built by newMCPSecurityEnv starts with no roots; register the
	// memory root exactly as runMCP does — straight into the rootSet, bypassing
	// index_folder's admission check (which would, correctly, refuse a dot-dir).
	env.rs.add(mcpRoot{Name: "memory", Path: memPath, Internal: true})

	out := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: rem.Path})
	if !strings.Contains(out.Content, "quokkazebra marker") {
		t.Fatalf("memory files must stay readable, got: %q", out.Content)
	}

	// The memory file is searchable too — filterExcludedItems must not eat it.
	// (Compare basenames: the indexer records the symlink-resolved path, while
	// remember reports the ~/.ney path as-configured.)
	found := false
	for _, r := range callTool[searchDocumentsOutput](t, env, "search_documents",
		searchDocumentsInput{Query: "quokkazebra marker"}).Results {
		if filepath.Base(r.Path) == filepath.Base(rem.Path) {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the memory file to survive the search-result deny filter")
	}

	// And the exemption is exactly the Internal bit — without it the same path
	// is denied, since ~/.ney is a dot-directory.
	var flt *pathfilter.Filter // nil: built-in rules only
	memFile := filepath.Join(memPath, filepath.Base(rem.Path))
	if !excludedForClient(flt, []mcpRoot{{Name: "memory", Path: memPath}}, memFile) {
		t.Fatal("a non-internal ~/.ney/memory root should be denied from $HOME — the Internal bit is what exempts it")
	}
	if excludedForClient(flt, []mcpRoot{{Name: "memory", Path: memPath, Internal: true}}, memFile) {
		t.Fatal("an internal memory root must be exempt")
	}
}

// --- M-2: extension guard ---------------------------------------------------------

func TestMCPReadDocumentRejectsUnsupportedExtension(t *testing.T) {
	env, _ := newMCPSecurityEnv(t)
	home := env.root

	folder := filepath.Join(home, "reports")
	writeTestFile(t, filepath.Join(folder, "q3.md"), "quarterly notes\n")
	writeTestFile(t, filepath.Join(folder, "data.sqlite"), "binary-ish payload\n")
	writeTestFile(t, filepath.Join(folder, "hosts.yml"), "oauth_token: gho_NOPE\n")
	writeTestFile(t, filepath.Join(folder, "notes.markdown"), "markdown variant\n")
	writeTestFile(t, filepath.Join(folder, "plain.txt"), "plain text\n")

	callTool[indexFolderOutput](t, env, "index_folder", indexFolderInput{Path: folder})

	for _, name := range []string{"data.sqlite", "hosts.yml"} {
		msg := callToolExpectError(t, env, "read_document", readDocumentInput{Path: filepath.Join(folder, name)})
		if !strings.Contains(msg, "unsupported file type") {
			t.Errorf("expected an unsupported-type rejection for %s, got: %s", name, msg)
		}
		if strings.Contains(msg, "gho_NOPE") {
			t.Errorf("leaked file content for %s: %s", name, msg)
		}
	}
	for _, name := range []string{"q3.md", "notes.markdown", "plain.txt"} {
		out := callTool[readDocumentOutput](t, env, "read_document", readDocumentInput{Path: filepath.Join(folder, name)})
		if out.Content == "" {
			t.Errorf("expected %s to still be readable", name)
		}
	}
}

// --- search-result leak -----------------------------------------------------------

// TestMCPSearchDocumentsFiltersDeniedWorkspace covers the third leak path: a
// dot-dir workspace that is already in the DB (old install, or `ney index
// ~/.aws` from the CLI) would still emit chunk snippets through
// search_documents even after read_document started refusing the same files.
func TestMCPSearchDocumentsFiltersDeniedWorkspace(t *testing.T) {
	env, ix := newMCPSecurityEnv(t)
	home := env.root

	const marker = "zebraquokka-credential-marker"
	dotRoot := filepath.Join(home, ".aws")
	writeTestFile(t, filepath.Join(dotRoot, "notes.md"), "aws login "+marker+"\n")
	if _, err := env.app.DB.UpsertWorkspace("aws", dotRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), dotRoot, "aws"); err != nil {
		t.Fatal(err)
	}

	// Sanity: it really is in the index (otherwise this test proves nothing).
	if n, err := env.app.DB.CountChunks(); err != nil || n == 0 {
		t.Fatalf("expected the dot-dir workspace to be indexed (chunks=%d, err=%v)", n, err)
	}

	out := callTool[searchDocumentsOutput](t, env, "search_documents", searchDocumentsInput{Query: marker})
	for _, r := range out.Results {
		t.Errorf("search_documents leaked a result from a denied workspace: %s / %s", r.Path, r.Snippet)
	}
}
