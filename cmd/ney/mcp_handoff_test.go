package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func handoffEnv(t *testing.T) (*mcpTestEnv, string) {
	t.Helper()
	repo := handoffFixture(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return connectMCPTestEnv(t, mcpDeps{cfg: cfg}), repo
}

func callText(t *testing.T, env *mcpTestEnv, name string, args any) string {
	t.Helper()
	res, err := env.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("tool %s error: %s", name, toolResultText(res))
	}
	return toolResultText(res)
}

func TestGetHandoff(t *testing.T) {
	env, repo := handoffEnv(t)
	out := callText(t, env, "get_handoff", map[string]any{"cwd": repo})
	for _, want := range []string{"Fix payment flow", "## Recent activity"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGetHandoffNoSessions(t *testing.T) {
	env, _ := handoffEnv(t)
	out := callText(t, env, "get_handoff", map[string]any{"cwd": t.TempDir()})
	if !strings.Contains(out, "no recent sessions for ") {
		t.Fatalf("got %q", out)
	}
}

func TestGetHandoffErrors(t *testing.T) {
	env, repo := handoffEnv(t)
	if msg := callToolExpectError(t, env, "get_handoff", map[string]any{"cwd": repo, "session_id": "nope"}); !strings.Contains(msg, "session nope not found") {
		t.Fatalf("got %q", msg)
	}
	for _, cwd := range []string{"", "relative/dir"} {
		callToolExpectError(t, env, "get_handoff", map[string]any{"cwd": cwd})
	}
}

func TestListSessions(t *testing.T) {
	env, repo := handoffEnv(t)
	writeSession(t, repo, "basic.jsonl", "s2", "Newer goal here", time.Now().Add(time.Hour))
	out := callText(t, env, "list_sessions", map[string]any{"cwd": repo})
	i, j := strings.Index(out, `"id": "s2"`), strings.Index(out, `"id": "s1"`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("want s2 before s1: %s", out)
	}
	one := callText(t, env, "list_sessions", map[string]any{"cwd": repo, "limit": 1})
	if strings.Contains(one, `"id": "s1"`) {
		t.Fatalf("limit ignored: %s", one)
	}
}

func TestWriteHandoffThenGet(t *testing.T) {
	env, repo := handoffEnv(t)
	out := callText(t, env, "write_handoff", map[string]any{"cwd": repo, "agent": "codex", "note": "REMAINING: wire up the refund webhook"})
	if !strings.Contains(out, `"path"`) {
		t.Fatalf("got %s", out)
	}
	got := callText(t, env, "get_handoff", map[string]any{"cwd": repo})
	i := strings.Index(got, "REMAINING: wire up the refund webhook")
	j := strings.Index(got, "Fix payment flow")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("note not first (note=%d session=%d):\n%s", i, j, got)
	}
}

func TestWriteHandoffRejectsOversize(t *testing.T) {
	env, repo := handoffEnv(t)
	ok := strings.Repeat("ก", 8000)
	callText(t, env, "write_handoff", map[string]any{"cwd": repo, "agent": "codex", "note": ok})
	callToolExpectError(t, env, "write_handoff", map[string]any{"cwd": repo, "agent": "codex", "note": ok + "ก"})
	callToolExpectError(t, env, "write_handoff", map[string]any{"cwd": "rel", "agent": "codex", "note": "x"})
}
