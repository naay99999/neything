package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/projects"
)

var commitSeq int

// commitN makes n commits in repo and returns HEAD before them ("" on an
// empty repo).
func commitN(t *testing.T, repo string, n int) string {
	t.Helper()
	before, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	for i := 0; i < n; i++ {
		commitSeq++
		f := filepath.Join(repo, fmt.Sprintf("f%05d.txt", commitSeq))
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "c"}} {
			if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	return strings.TrimSpace(string(before))
}

func headOf(t *testing.T, repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func hookContext(t *testing.T, repo string) string {
	t.Helper()
	out, _, err := execHook(t, hookStdin(t, repo, "startup", "new-session"), "--agent", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		return ""
	}
	var got hookOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	return got.HookSpecificOutput.AdditionalContext
}

func registerOther(t *testing.T, name string) {
	t.Helper()
	dir := projectsDir()
	r, _ := projects.LoadRegistry(dir)
	r.Upsert(projects.Entry{Path: filepath.Join(t.TempDir(), name), LastSeen: time.Now()})
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
}

func writeCard(t *testing.T, repo string, c projects.Card) {
	t.Helper()
	dir := projectsDir()
	r, _ := projects.LoadRegistry(dir)
	e := r.Upsert(projects.Entry{Path: repo, LastSeen: time.Now()})
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	c.Name, c.Path = e.Name, repo
	if err := projects.Save(projects.CardPath(dir, e.Name), c); err != nil {
		t.Fatal(err)
	}
}

func TestHookProjectsMissingCard(t *testing.T) {
	repo := handoffFixture(t)
	registerOther(t, "portfolio")
	ctx := hookContext(t, repo)
	for _, want := range []string{"Fix payment flow", "## ney — projects\n", ": portfolio\n",
		"This project has no ney card yet. Once you understand it, call update_project."} {
		if !strings.Contains(ctx, want) {
			t.Errorf("missing %q in:\n%s", want, ctx)
		}
	}
	if strings.Contains(ctx, "payrepo,") || strings.Contains(ctx, ", payrepo") {
		t.Errorf("current repo listed among other projects:\n%s", ctx)
	}
	r, _ := projects.LoadRegistry(projectsDir())
	if _, ok := r.Lookup(repo, ""); !ok {
		t.Error("current repo not registered")
	}
}

func TestHookProjectsStaleCard(t *testing.T) {
	repo := handoffFixture(t)
	commitN(t, repo, 1)
	writeCard(t, repo, projects.Card{Summary: "s", Commit: headOf(t, repo), Updated: time.Now()})
	commitN(t, repo, 30)
	ctx := hookContext(t, repo)
	if !strings.Contains(ctx, "This project's ney card is out of date (30 commits since it was written)") {
		t.Errorf("no stale line:\n%s", ctx)
	}
}

func TestHookProjectsFresh(t *testing.T) {
	repo := handoffFixture(t)
	commitN(t, repo, 1)
	writeCard(t, repo, projects.Card{Summary: "s", Commit: headOf(t, repo), Updated: time.Now()})
	ctx := hookContext(t, repo)
	if strings.Contains(ctx, "ney card") {
		t.Errorf("fresh card nagged:\n%s", ctx)
	}
}

func TestHookBrokenCardIsMissing(t *testing.T) {
	repo := handoffFixture(t)
	writeCard(t, repo, projects.Card{Summary: "s"})
	r, _ := projects.LoadRegistry(projectsDir())
	e, _ := r.Lookup(repo, "")
	if err := os.WriteFile(projects.CardPath(projectsDir(), e.Name), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ctx := hookContext(t, repo); !strings.Contains(ctx, "no ney card yet") {
		t.Errorf("broken card not treated as missing:\n%s", ctx)
	}
}

func TestHookProjectsDisabled(t *testing.T) {
	repo := handoffFixture(t)
	writeNeyConfig(t, "projects:\n  inject: false\n")
	registerOther(t, "portfolio")
	ctx := hookContext(t, repo)
	if strings.Contains(ctx, "## ney — projects") || !strings.Contains(ctx, "Fix payment flow") {
		t.Errorf("inject:false:\n%s", ctx)
	}
}

func TestHookProjectsGitFailure(t *testing.T) {
	repo := handoffFixture(t)
	commitN(t, repo, 1)
	writeCard(t, repo, projects.Card{Summary: "s", Commit: "deadbeef", Updated: time.Now()})
	if ctx := hookContext(t, repo); strings.Contains(ctx, "ney card") {
		t.Errorf("unknown commit should count as fresh:\n%s", ctx)
	}
}

func TestHookDisabledRepoNotRegistered(t *testing.T) {
	repo := handoffFixture(t)
	writeNeyConfig(t, "handoff:\n  disabled_repos: ['"+repo+"']\n")
	if ctx := hookContext(t, repo); ctx != "" {
		t.Errorf("disabled repo produced output:\n%s", ctx)
	}
	if _, err := os.Stat(filepath.Join(projectsDir(), ".registry.json")); !os.IsNotExist(err) {
		t.Errorf("registry written for a disabled repo: %v", err)
	}
}

// TestHookProjectsGitCalls pins the spec's budget: with the repo already
// registered and a card present, the projects part adds at most two git calls.
func TestHookProjectsGitCalls(t *testing.T) {
	repo := handoffFixture(t)
	commitN(t, repo, 1)
	writeCard(t, repo, projects.Card{Summary: "s", Commit: headOf(t, repo), Updated: time.Now()})
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE", trace)
	hookProjects(context.Background(), cfg, repo, true, time.Now())
	b, _ := os.ReadFile(trace)
	if n := strings.Count(string(b), "built-in: git "); n > 2 {
		t.Errorf("projects part ran %d git commands, want ≤ 2:\n%s", n, b)
	}
}

func TestHookSkipsRegistryWhenLocked(t *testing.T) {
	repo := handoffFixture(t)
	registerOther(t, "portfolio")
	if err := os.WriteFile(filepath.Join(projectsDir(), ".registry.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ctx := hookContext(t, repo)
	if time.Since(start) > time.Second || !strings.Contains(ctx, ": portfolio\n") {
		t.Errorf("locked registry: took %v, context:\n%s", time.Since(start), ctx)
	}
}

func TestCollectItemsUsesRemoteAndSkipsDisabled(t *testing.T) {
	setHome(t, t.TempDir())
	base := t.TempDir()
	moved := filepath.Join(base, "new", "x")
	initGitFixtureRepo(t, moved, "c")
	if out, err := exec.Command("git", "-C", moved, "remote", "add", "origin", "git@github.com:u/x.git").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	secret := filepath.Join(base, "secret")
	initGitFixtureRepo(t, secret, "c")
	if _, err := projects.UpdateRegistry(projectsDir(), time.Second, func(r *projects.Registry) {
		r.Upsert(projects.Entry{Path: filepath.Join(base, "old", "x"), Remote: "github.com/u/x", LastSeen: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	writeNeyConfig(t, "handoff:\n  disabled_repos: ['"+secret+"']\n")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	scan := neycontext.ScanRepos(context.Background(), []string{base})
	collectItems(context.Background(), cfg, scan, time.Now())
	r, _ := projects.LoadRegistry(projectsDir())
	if len(r.Repos) != 1 || r.Repos[0].Name != "x" || !strings.HasSuffix(r.Repos[0].Path, filepath.Join("new", "x")) {
		t.Errorf("registry = %+v, want the moved repo re-linked and the disabled one absent", r.Repos)
	}
}
