package neycontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", "change "+name)
}

func newCommittedRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	commitFile(t, dir, "x.go", "package x\n")
	return dir
}

func TestRepoRemoteNormalises(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Naay/X.git":           "github.com/Naay/X",
		"https://user:tok@GitHub.com/a/b.git": "github.com/a/b",
		"ssh://git@host:22/a/b":               "host:22/a/b",
		"https://gitlab.example.com/g/sub/r/": "gitlab.example.com/g/sub/r",
	}
	for in, want := range cases {
		if got := normaliseRemote(in); got != want {
			t.Errorf("normaliseRemote(%q) = %q, want %q", in, got, want)
		}
	}
	repo := newCommittedRepo(t)
	ctx := context.Background()
	if got := RepoRemote(ctx, repo); got != "" {
		t.Errorf("no remote: got %q", got)
	}
	gitT(t, repo, "remote", "add", "origin", "git@github.com:naay99999/neything.git")
	if got := RepoRemote(ctx, repo); got != "github.com/naay99999/neything" {
		t.Errorf("RepoRemote = %q", got)
	}
}

func TestCardDrift(t *testing.T) {
	repo := newCommittedRepo(t)
	ctx := context.Background()
	a := HeadCommit(ctx, repo)
	if len(a) != 40 {
		t.Fatalf("HeadCommit = %q", a)
	}
	commitFile(t, repo, "go.mod", "module x\n")
	commitFile(t, repo, "x.go", "package x // 2\n")
	commitFile(t, repo, "y.go", "package x\n")
	files := []string{"go.mod", "README.md"}
	commits, changed, known, err := CardDrift(ctx, repo, a, files)
	if err != nil || !known || commits != 3 || !reflect.DeepEqual(changed, []string{"go.mod"}) {
		t.Fatalf("CardDrift = %d %v %v %v", commits, changed, known, err)
	}
	if _, _, known, err := CardDrift(ctx, repo, "deadbeef", files); known || err != nil {
		t.Errorf("unknown commit: known=%v err=%v", known, err)
	}
	if _, _, known, err := CardDrift(ctx, repo, "", files); known || err != nil {
		t.Errorf("empty since: known=%v err=%v", known, err)
	}
}

func TestLastCommit(t *testing.T) {
	repo := newCommittedRepo(t)
	commitFile(t, repo, "y.go", "package x\n")
	subject, at, err := LastCommit(context.Background(), repo)
	if err != nil || subject != "change y.go" || at.IsZero() {
		t.Fatalf("LastCommit = %q %v %v", subject, at, err)
	}
}
