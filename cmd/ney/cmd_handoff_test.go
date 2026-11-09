package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	setHome(t, home)
	repo := filepath.Join(home, "work", "payrepo")
	gitInit(t, repo)
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "session", "claudecode", "testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// "/repo/" first so the bare "/repo" replacement can't double-rewrite.
	data := strings.ReplaceAll(string(raw), "/repo/", jsonPath(repo)+"/")
	data = strings.ReplaceAll(data, `"cwd":"/repo"`, `"cwd":"`+jsonPath(repo)+`"`)
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

// writeSession drops a copy of a claudecode testdata transcript into repo's
// project dir, with cwd rewritten, goal optionally replaced, and mtime set.
func writeSession(t *testing.T, repo, fixture, id, goal string, mtime time.Time) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "session", "claudecode", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	data := strings.ReplaceAll(string(raw), "/repo/", jsonPath(repo)+"/")
	data = strings.ReplaceAll(data, `"cwd":"/repo"`, `"cwd":"`+jsonPath(repo)+`"`)
	if goal != "" {
		data = strings.ReplaceAll(data, "Fix the payment flow so refunds work", goal)
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".claude", "projects", claudecode.Slug(repo), id+".jsonl")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// A requested session that exists but is not a real session must fail, not
// silently hand over a different session's brief.
func TestHandoffSessionNotASession(t *testing.T) {
	repo := handoffFixture(t)
	writeSession(t, repo, "metaonly.jsonl", "s3", "", time.Now())
	out, _, err := execHandoff(t, repo, "--session", "s3")
	if err == nil || !strings.Contains(err.Error(), "session s3 not found for payrepo") {
		t.Errorf("err=%v out=%q", err, out)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
}

func TestBuildBriefExcludeSessionID(t *testing.T) {
	repo := handoffFixture(t)
	now := time.Now()
	writeSession(t, repo, "basic.jsonl", "s1", "", now.Add(-48*time.Hour))
	writeSession(t, repo, "basic.jsonl", "s2", "Second session goal", now)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := buildBrief(context.Background(), cfg, handoffRequest{Cwd: repo, Now: now}, false)
	if err != nil || b.Primary == nil || b.Primary.ID != "s2" {
		t.Fatalf("baseline: err=%v primary=%+v", err, b.Primary)
	}
	b, _, err = buildBrief(context.Background(), cfg, handoffRequest{Cwd: repo, Now: now, ExcludeSessionID: "s2"}, false)
	if err != nil || b.Primary == nil || b.Primary.ID != "s1" {
		t.Fatalf("exclude s2: err=%v primary=%+v", err, b.Primary)
	}
	if len(b.Others) != 0 {
		t.Errorf("Others = %d, excluded session leaked", len(b.Others))
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

// jsonPath escapes a filesystem path for a hand-built JSON string literal
// (Windows paths carry backslashes).
func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
