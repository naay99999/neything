package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/projects"
)

func TestUpdateThenGetProject(t *testing.T) {
	env := newMCPTestEnv(t)
	repo := filepath.Join(t.TempDir(), "neything")
	initGitFixtureRepo(t, repo, "first commit")
	callText(t, env, "update_project", map[string]any{
		"cwd": repo, "agent": "claude-code",
		"summary": "Session handoff CLI. Reads transcripts.", "stack": "Go", "facts_add": []string{"no LLM calls"},
	})
	out := callText(t, env, "get_project", map[string]any{"name": "neything"})
	for _, want := range []string{"Session handoff CLI. Reads transcripts.", "stack: Go", "- no LLM calls",
		"updated_by: claude-code", "Live: branch", `"first commit"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	b, _ := os.ReadFile(filepath.Join(projectsDir(), "index.md"))
	if !strings.Contains(string(b), "[[neything]] — Session handoff CLI.") {
		t.Errorf("index.md:\n%s", b)
	}
	c, err := projects.Load(projects.CardPath(projectsDir(), "neything"))
	real, _ := filepath.EvalSymlinks(repo)
	if err != nil || c.Commit == "" || c.Path != real {
		t.Errorf("card = %+v, %v", c, err)
	}
	// a patch touches only what it names
	callText(t, env, "update_project", map[string]any{"cwd": repo, "agent": "codex", "status": "paused"})
	if out := callText(t, env, "get_project", map[string]any{"name": "neything"}); !strings.Contains(out, "stack: Go") ||
		!strings.Contains(out, "status: paused") {
		t.Errorf("patch lost fields:\n%s", out)
	}
}

func TestUpdateProjectCapError(t *testing.T) {
	env := newMCPTestEnv(t)
	repo := filepath.Join(t.TempDir(), "capped")
	initGitFixtureRepo(t, repo, "c")
	msg := callToolExpectError(t, env, "update_project", map[string]any{
		"cwd": repo, "agent": "a", "summary": strings.Repeat("ก", 301)})
	if !strings.Contains(msg, "summary is 301 characters; the limit is 300") {
		t.Errorf("msg = %q", msg)
	}
	if _, err := os.Stat(projects.CardPath(projectsDir(), "capped")); !os.IsNotExist(err) {
		t.Errorf("card written despite the error: %v", err)
	}
}

func TestUpdateProjectNotRepo(t *testing.T) {
	env := newMCPTestEnv(t)
	msg := callToolExpectError(t, env, "update_project", map[string]any{"cwd": t.TempDir(), "agent": "a", "summary": "s"})
	if !strings.Contains(msg, "cwd is not inside a git repository") {
		t.Errorf("msg = %q", msg)
	}
	if msg := callToolExpectError(t, env, "update_project", map[string]any{"cwd": "rel", "agent": "a"}); !strings.Contains(msg, "absolute") {
		t.Errorf("relative cwd: %q", msg)
	}
}

func TestGetProjectUnknown(t *testing.T) {
	env := newMCPTestEnv(t)
	repo := filepath.Join(t.TempDir(), "neything")
	initGitFixtureRepo(t, repo, "c")
	callText(t, env, "update_project", map[string]any{"cwd": repo, "agent": "a", "summary": "s"})
	msg := callToolExpectError(t, env, "get_project", map[string]any{"name": "neyth"})
	if !strings.Contains(msg, `no project named "neyth"; closest: neything`) {
		t.Errorf("msg = %q", msg)
	}
}

func TestListProjects(t *testing.T) {
	env := newMCPTestEnv(t)
	a := filepath.Join(t.TempDir(), "alpha")
	b := filepath.Join(t.TempDir(), "beta")
	initGitFixtureRepo(t, a, "c")
	initGitFixtureRepo(t, b, "c")
	callText(t, env, "update_project", map[string]any{"cwd": a, "agent": "x", "summary": "Alpha does things."})
	reg, _ := projects.LoadRegistry(projectsDir())
	reg.Upsert(projects.Entry{Path: b, LastSeen: time.Now()})
	if err := reg.Save(projectsDir()); err != nil {
		t.Fatal(err)
	}
	out := callText(t, env, "list_projects", map[string]any{})
	for _, want := range []string{"## Who you're working with", "## Projects", "- alpha · ", "Alpha does things.", "- beta · ", "(no card yet)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
