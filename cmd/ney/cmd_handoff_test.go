package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/session/claudecode"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// handoffFixture builds a fake HOME with one Claude Code session whose cwd is
// a real temp git repo, and returns the repo root.
func handoffFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "work", "payrepo")
	gitInit(t, repo)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "session", "claudecode", "testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// "/repo/" first so the bare "/repo" replacement can't double-rewrite.
	data := strings.ReplaceAll(string(raw), "/repo/", repo+"/")
	data = strings.ReplaceAll(data, `"cwd":"/repo"`, `"cwd":"`+repo+`"`)
	dir := filepath.Join(home, ".claude", "projects", claudecode.Slug(repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func execHandoff(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(cwd)
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"handoff"}, args...))
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	err = rootCmd.Execute()
	return out.String(), errb.String(), err
}

func TestHandoffCommand(t *testing.T) {
	repo := handoffFixture(t)

	out, _, err := execHandoff(t, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Fix payment flow", "Ended with:"} {
		if !strings.Contains(out, want) {
			t.Errorf("inject output missing %q:\n%s", want, out)
		}
	}
	// Absolute file paths inside the repo are shown repo-relative.
	if strings.Contains(out, repo+"/a.go") {
		t.Errorf("absolute path leaked into brief:\n%s", out)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("relative file path missing:\n%s", out)
	}

	full, _, err := execHandoff(t, repo, "--full")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "## Recent activity") {
		t.Errorf("--full output missing Recent activity:\n%s", full)
	}

	// A subdirectory resolves to the same repo.
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _, err := execHandoff(t, sub); err != nil || !strings.Contains(out, "Fix payment flow") {
		t.Errorf("subdir run: err=%v out=%q", err, out)
	}

	// --session: explicit id works, unknown id fails.
	if out, _, err := execHandoff(t, repo, "--session", "s1"); err != nil || !strings.Contains(out, "Fix payment flow") {
		t.Errorf("--session s1: err=%v out=%q", err, out)
	}
	_, stderr, err := execHandoff(t, repo, "--session", "nope")
	if err == nil || !strings.Contains(err.Error(), "session nope not found for payrepo") {
		t.Errorf("--session nope: err=%v stderr=%q", err, stderr)
	}
}

func TestHandoffCommandNoSessions(t *testing.T) {
	repo := handoffFixture(t)
	other := filepath.Join(filepath.Dir(repo), "emptyrepo")
	gitInit(t, other)
	stdout, stderr, err := execHandoff(t, other)
	if err != nil {
		t.Fatalf("want exit 0, got %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "no recent sessions for emptyrepo") {
		t.Errorf("stderr = %q", stderr)
	}
}
