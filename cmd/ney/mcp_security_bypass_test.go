package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Adversarial pass: bypass variants for the dot-directory admission check
// that a straightforward implementation would miss.
func TestVerifyDotDirBypassVariants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	secret := filepath.Join(home, ".config", "gh", "hosts.yml")
	writeTestFile(t, secret, "oauth_token: gho_MARKER1\n")

	// A legit, non-hidden root that hides a dot-dir underneath it.
	work := filepath.Join(home, "work")
	writeTestFile(t, filepath.Join(work, "readme.md"), "public notes\n")
	writeTestFile(t, filepath.Join(work, ".hidden", "creds.md"), "token: gho_MARKER2\n")

	// Symlink laundering: a normal-looking name pointing at a dot-dir.
	link := filepath.Join(home, "shortcut")
	if err := os.Symlink(filepath.Join(home, ".config"), link); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	app, err := initApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.DB.Close() })

	ix, err := newIndexer(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	server := newMCPServer(mcpDeps{
		app: app, cfg: cfg, state: newServerState(nil, false), rs: newRootSet(nil),
		flt: newPathFilter(cfg), ix: ix,
		serialize:    func(f func()) { mu.Lock(); defer mu.Unlock(); f() },
		startWatcher: func(mcpRoot, int64, bool) {},
	})
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "bypass", Version: "v0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool %s: %v", name, err)
		}
		return res
	}
	mustDeny := func(label string, res *mcp.CallToolResult) {
		t.Helper()
		if !res.IsError {
			t.Errorf("BYPASS [%s]: call succeeded: %s", label, vtext(res))
			return
		}
		t.Logf("%s -> denied: %s", label, vtext(res))
	}

	// 1. symlink laundering into a dot-dir
	mustDeny("index_folder via symlink", call("index_folder", map[string]any{"path": link}))
	mustDeny("search_folder via symlink", call("search_folder", map[string]any{"path": link, "query": "oauth token"}))

	// 2. .. traversal that lands on a dot-dir
	mustDeny("index_folder via ..", call("index_folder",
		map[string]any{"path": filepath.Join(work, "..", ".config")}))

	// 3. dot-dir nested below an allowed root must still be unreachable
	if r := call("index_folder", map[string]any{"path": work}); r.IsError {
		t.Fatalf("legit folder should index: %s", vtext(r))
	}
	r := call("read_document", map[string]any{"path": filepath.Join(work, ".hidden", "creds.md")})
	if strings.Contains(vtext(r), "gho_MARKER2") {
		t.Errorf("BYPASS: dot-dir below an allowed root was served: %s", vtext(r))
	} else {
		t.Logf("dot-dir below root -> denied: %s", vtext(r))
	}

	// 4. no marker may appear in any search output
	for _, q := range []string{"oauth", "token", "gho"} {
		out := vtext(call("search_documents", map[string]any{"query": q}))
		if strings.Contains(out, "gho_MARKER") {
			t.Errorf("BYPASS: search_documents leaked a marker for %q: %s", q, out)
		}
	}
}

// The memory workspace is exempted from the $HOME-based deny rule so that
// `remember` keeps working. Verify that exemption cannot be widened to reach
// the rest of ~/.ney (config.yaml holds no secrets today, but the writer lock
// and the index live there too).
func TestVerifyMemoryRootCannotEscapeNeyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	neyDir := filepath.Join(home, ".ney")
	memDir := filepath.Join(neyDir, "memory")
	writeTestFile(t, filepath.Join(memDir, "2026-08-01-note.md"), "remembered fact\n")
	writeTestFile(t, filepath.Join(neyDir, "config.yaml"), "embedder:\n  provider: none\n")
	writeTestFile(t, filepath.Join(neyDir, "sneaky.md"), "SHOULD_NOT_BE_SERVED\n")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	app, err := initApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.DB.Close() })

	resolvedMem, err := filepath.EvalSymlinks(memDir)
	if err != nil {
		t.Fatal(err)
	}
	roots := []mcpRoot{{Name: "memory", Path: resolvedMem, Internal: true}}
	server := newMCPServer(mcpDeps{
		app: app, cfg: cfg, state: newServerState(rootNames(roots), false),
		rs: newRootSet(roots), flt: newPathFilter(cfg),
	})
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "mem", Version: "v0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	read := func(p string) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "read_document", Arguments: map[string]any{"path": p}})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		return res
	}

	// The exemption must still work.
	if got := vtext(read(filepath.Join(memDir, "2026-08-01-note.md"))); !strings.Contains(got, "remembered fact") {
		t.Errorf("memory file should be readable, got: %s", got)
	}

	// ...but must not extend to the rest of ~/.ney, including via traversal.
	for _, p := range []string{
		filepath.Join(neyDir, "sneaky.md"),
		filepath.Join(memDir, "..", "sneaky.md"),
		filepath.Join(neyDir, "config.yaml"),
	} {
		if got := vtext(read(p)); strings.Contains(got, "SHOULD_NOT_BE_SERVED") || strings.Contains(got, "provider") {
			t.Errorf("BYPASS: served %s outside the memory root: %s", p, got)
		}
	}
}
