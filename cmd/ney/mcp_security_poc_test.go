package main

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Independent re-run of the two PoCs that succeeded against the pre-fix code.
// Deliberately written from scratch against the MCP tool surface only, so it
// does not share helpers (or assumptions) with the fix author's own tests.

func vtext(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestVerifyOriginalPoCsAreClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ghSecret := filepath.Join(home, ".config", "gh", "hosts.yml")
	writeTestFile(t, ghSecret, "github.com:\n  oauth_token: gho_SUPERSECRET\n")
	dockerSecret := filepath.Join(home, ".docker", "config.json")
	writeTestFile(t, dockerSecret, "{\n \"auths\": {\"registry.io\": {\"auth\": \"aGFja2VyOnAtYS1z-c3dvcmQ=\"}}\n}\n")

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
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "verify", Version: "v0"}, nil).Connect(ctx, t2, nil)
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

	// PoC 1 (H-1): index_folder on a dot-dir, then read the secret.
	r1 := call("index_folder", map[string]any{"path": filepath.Join(home, ".config")})
	if !r1.IsError {
		t.Errorf("H-1 NOT FIXED: index_folder(~/.config) succeeded: %s", vtext(r1))
	}
	r2 := call("read_document", map[string]any{"path": ghSecret})
	if strings.Contains(vtext(r2), "gho_SUPERSECRET") {
		t.Errorf("H-1 NOT FIXED: read_document leaked the token: %s", vtext(r2))
	}

	// PoC 2 (H-2): search_folder content-grep on a dot-dir.
	r3 := call("search_folder", map[string]any{"path": filepath.Join(home, ".docker"), "query": "auths registry"})
	if strings.Contains(vtext(r3), "aGFja2Vy") {
		t.Errorf("H-2 NOT FIXED: search_folder leaked registry auth: %s", vtext(r3))
	}

	// The user's own home scan must not surface dot-dir content either.
	r4 := call("search_folder", map[string]any{"path": home, "query": "auths registry oauth"})
	if strings.Contains(vtext(r4), "aGFja2Vy") || strings.Contains(vtext(r4), "gho_SUPER") {
		t.Errorf("H-2 NOT FIXED: scanning $HOME reached into dot-dirs: %s", vtext(r4))
	}

	t.Logf("index_folder(dotdir): %s", vtext(r1))
	t.Logf("read_document(secret): %s", vtext(r2))
	t.Logf("search_folder(dotdir): %s", vtext(r3))
}
