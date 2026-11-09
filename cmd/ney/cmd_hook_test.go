package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/session/claudecode"
)

func execHook(tb testing.TB, stdin string, args ...string) (stdout, stderr string, err error) {
	tb.Helper()
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"hook", "session-start"}, args...))
	tb.Cleanup(func() { rootCmd.SetIn(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

func hookStdin(tb testing.TB, cwd, source, sessionID string) string {
	tb.Helper()
	b, err := json.Marshal(map[string]string{
		"session_id": sessionID, "cwd": cwd, "source": source, "hook_event_name": "SessionStart",
	})
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

func writeNeyConfig(t *testing.T, body string) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ney")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHookInjects(t *testing.T) {
	repo := handoffFixture(t)
	for _, source := range []string{"startup", "clear", ""} {
		out, _, err := execHook(t, hookStdin(t, repo, source, "new-session"), "--agent", "claude-code")
		if err != nil {
			t.Fatal(err)
		}
		var got hookOutput
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("source %q: stdout not JSON: %v\n%s", source, err, out)
		}
		if got.HookSpecificOutput.HookEventName != "SessionStart" {
			t.Errorf("source %q: hookEventName = %q", source, got.HookSpecificOutput.HookEventName)
		}
		if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "Fix payment flow") {
			t.Errorf("source %q: context missing goal:\n%s", source, got.HookSpecificOutput.AdditionalContext)
		}
	}
	// An unknown agent is not an error.
	if out, _, err := execHook(t, hookStdin(t, repo, "startup", "new-session"), "--agent", "mystery"); err != nil || out == "" {
		t.Errorf("unknown agent: err=%v out=%q", err, out)
	}
}

func TestHookExcludesCurrentSession(t *testing.T) {
	repo := handoffFixture(t)
	out, _, err := execHook(t, hookStdin(t, repo, "startup", "s1"))
	if err != nil || out != "" {
		t.Errorf("err=%v out=%q, want empty", err, out)
	}
}

func TestHookSkips(t *testing.T) {
	cases := []struct {
		name, source string
		config       func(repo string) string
	}{
		{"resume", "resume", nil},
		{"compact", "compact", nil},
		{"fork", "fork", nil},
		{"auto false", "startup", func(string) string { return "handoff:\n  auto: false\n" }},
		{"disabled repo", "startup", func(r string) string { return "handoff:\n  disabled_repos: ['" + r + "']\n" }},
		{"disabled parent", "startup", func(r string) string { return "handoff:\n  disabled_repos: ['" + filepath.Dir(r) + "/']\n" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := handoffFixture(t)
			if tc.config != nil {
				writeNeyConfig(t, tc.config(repo))
			}
			out, _, err := execHook(t, hookStdin(t, repo, tc.source, "new-session"))
			if err != nil || out != "" {
				t.Errorf("err=%v out=%q, want empty", err, out)
			}
		})
	}
}

func TestHookNeverFails(t *testing.T) {
	repo := handoffFixture(t)
	for name, stdin := range map[string]string{
		"not json":   "not json",
		"empty":      "",
		"bad cwd":    hookStdin(t, "/nonexistent", "startup", "x"),
		"json array": "[1,2]",
	} {
		out, _, err := execHook(t, stdin)
		if err != nil || out != "" {
			t.Errorf("%s: err=%v out=%q, want exit 0 and empty stdout", name, err, out)
		}
	}
	writeNeyConfig(t, "handoff: [unclosed\n  : :\n")
	out, stderr, err := execHook(t, hookStdin(t, repo, "startup", "x"))
	if err != nil || out != "" {
		t.Errorf("bad config: err=%v out=%q", err, out)
	}
	if stderr == "" {
		t.Error("bad config: want a stderr diagnostic")
	}
}

func TestHookOutsideGit(t *testing.T) {
	repo := handoffFixture(t) // sets fake HOME
	dir := filepath.Join(filepath.Dir(repo), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", claudecode.Slug(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, dir, "basic.jsonl", "p1", "", time.Now())
	out, _, err := execHook(t, hookStdin(t, dir, "startup", "new-session"))
	if err != nil {
		t.Fatal(err)
	}
	var got hookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "Fix payment flow") {
		t.Errorf("brief missing:\n%s", ctx)
	}
	if strings.Contains(ctx, "Now:") {
		t.Errorf("Now: line present outside git:\n%s", ctx)
	}
}

func TestHookStdinNeverClosedReturns(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetIn(pr)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs([]string{"hook", "session-start"})
	t.Cleanup(func() { rootCmd.SetIn(nil); rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- rootCmd.Execute() }()
	select {
	case err := <-done:
		if err != nil || out.Len() != 0 {
			t.Errorf("err=%v out=%q", err, out.String())
		}
		if time.Since(start) > 2500*time.Millisecond {
			t.Errorf("took %s", time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook hung on open stdin")
	}
}

func TestHookRepoDisabledSymlinkAndEmpty(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !hookRepoDisabled([]string{link}, real, real) {
		t.Error("symlinked disabled entry not matched against resolved root")
	}
	if hookRepoDisabled([]string{"", "  "}, real, real) {
		t.Error("empty disabled entry matched")
	}
}
