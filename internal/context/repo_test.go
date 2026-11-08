package neycontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// repoGit runs git in dir with an environment isolated from the user's
// global/system config.
func repoGit(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	if date != "" {
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func evalDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func newRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	root := filepath.Join(evalDir(t, t.TempDir()), "main")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	repoGit(t, root, "", "init", "-q", "-b", "main")
	return root
}

func TestResolveRepoSubdir(t *testing.T) {
	root := newRepo(t)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := ResolveRepo(context.Background(), sub)
	if !ok || evalDir(t, got) != evalDir(t, root) {
		t.Errorf("ResolveRepo = (%q, %v), want (%q, true)", got, ok, root)
	}
}

func TestResolveRepoWorktree(t *testing.T) {
	root := newRepo(t)
	repoGit(t, root, "2026-01-01T00:00:00Z", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(filepath.Dir(root), "wt")
	repoGit(t, root, "", "worktree", "add", "-q", wt, "-b", "feat")
	got, ok := ResolveRepo(context.Background(), wt)
	if !ok || evalDir(t, got) != evalDir(t, root) {
		t.Errorf("ResolveRepo(worktree) = (%q, %v), want (%q, true)", got, ok, root)
	}
}

func TestResolveRepoNotGit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	got, ok := ResolveRepo(context.Background(), dir)
	if ok || got != filepath.Clean(dir) {
		t.Errorf("ResolveRepo = (%q, %v), want (%q, false)", got, ok, dir)
	}
}

func TestRepoState(t *testing.T) {
	root := newRepo(t)
	now := time.Now().UTC().Truncate(time.Second)
	repoGit(t, root, now.Add(-2*time.Hour).Format(time.RFC3339), "commit", "-q", "--allow-empty", "-m", "one")
	repoGit(t, root, now.Add(-30*time.Minute).Format(time.RFC3339), "commit", "-q", "--allow-empty", "-m", "two")
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := RepoState(context.Background(), root, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := GitState{Branch: "main", Uncommitted: 1, CommitsSince: 1}
	if got != want {
		t.Errorf("RepoState = %+v, want %+v", got, want)
	}

	got, err = RepoState(context.Background(), root, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got.CommitsSince != 0 {
		t.Errorf("zero since: CommitsSince = %d, want 0", got.CommitsSince)
	}
}
