package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	neycontext "github.com/naay99999/neything/internal/context"
)

// mcpTestEnv is an in-process MCP client/server pair
// (mcp.NewInMemoryTransports) over an isolated temp HOME, without touching
// stdio.
type mcpTestEnv struct {
	cs *mcp.ClientSession
}

func newMCPTestEnv(t *testing.T) *mcpTestEnv {
	t.Helper()
	setHome(t, t.TempDir())

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return connectMCPTestEnv(t, mcpDeps{cfg: cfg, scanCache: &neycontext.ScanCache{}})
}

func connectMCPTestEnv(t *testing.T, deps mcpDeps) *mcpTestEnv {
	t.Helper()
	server := newMCPServer(deps)

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
	return &mcpTestEnv{cs: cs}
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
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal structured content for %s into %T: %v (raw: %s)", name, out, err, b)
	}
	return out
}

// callToolExpectError calls a tool expecting it to fail (either a
// protocol-level error, e.g. bad input, or a tool-level IsError result) and
// returns a combined error message.
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

func TestMCPListsTools(t *testing.T) {
	env := newMCPTestEnv(t)

	res, err := env.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	want := []string{"get_handoff", "get_project", "list_projects", "list_sessions", "update_profile", "update_project", "write_handoff"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

// requireGitForMCPTest skips the test if git isn't on PATH — ScanRepos
// shells out to git, matching internal/context's own test guard.
func requireGitForMCPTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// initGitFixtureRepo creates a one-commit git repo at dir, for tests that
// exercise ScanRepos indirectly through get_context.
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

func TestMCPUpdateProfileRoundTrip(t *testing.T) {
	env := newMCPTestEnv(t)

	upOut := callTool[updateProfileOutput](t, env, "update_profile", updateProfileInput{
		Section: "Current focus",
		Content: "Building the layered context MCP surface.",
	})
	if upOut.Path == "" {
		t.Fatal("expected a non-empty path")
	}
	data, err := os.ReadFile(upOut.Path)
	if err != nil {
		t.Fatalf("expected profile.md to exist: %v", err)
	}
	if !strings.Contains(string(data), "Building the layered context MCP surface.") {
		t.Fatalf("expected profile.md to contain the update, got: %s", data)
	}

	if out := callText(t, env, "list_projects", map[string]any{}); !strings.Contains(out, "Building the layered context MCP surface.") {
		t.Fatalf("expected list_projects to reflect the profile update, got: %s", out)
	}
}

func TestMCPUpdateProfileRequiresSectionAndContent(t *testing.T) {
	env := newMCPTestEnv(t)
	if msg := callToolExpectError(t, env, "update_profile", updateProfileInput{Content: "x"}); !strings.Contains(msg, "section is required") {
		t.Fatalf("expected 'section is required', got: %s", msg)
	}
	if msg := callToolExpectError(t, env, "update_profile", updateProfileInput{Section: "x"}); !strings.Contains(msg, "content is required") {
		t.Fatalf("expected 'content is required', got: %s", msg)
	}
}

// TestMCPStdoutHygiene pins the invariant that stdout is reserved for the
// MCP protocol: serving tool calls must write nothing to os.Stdout.
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
	repo := filepath.Join(t.TempDir(), "hyg")
	initGitFixtureRepo(t, repo, "c")
	_ = callText(t, env, "list_projects", map[string]any{})
	_ = callText(t, env, "update_project", map[string]any{"cwd": repo, "agent": "test", "summary": "s"})
	_ = callText(t, env, "get_project", map[string]any{"name": "hyg"})
	_ = callTool[updateProfileOutput](t, env, "update_profile", updateProfileInput{Section: "s", Content: "c"})

	restore()

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if n > 0 {
		t.Fatalf("expected zero bytes on stdout, got %d: %q", n, buf[:n])
	}
}
