package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// requireGit skips the test when git isn't on PATH — ScanRepos degrades to
// an empty list in that case, same convention as internal/context's tests.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initRepo creates a git repo at dir with the given files committed.
func initRepo(t *testing.T, dir string, files ...string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	for _, f := range files {
		write(t, filepath.Join(dir, f))
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
}

func TestDiscoverFindsRepoAndCountsDocs(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	initRepo(t, repo, "README.md", "notes.txt", "src/main.go")

	cands, err := Discover(context.Background(), Options{Roots: []string{root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", cands)
	}
	c := cands[0]
	if c.Path != repo {
		t.Errorf("Path = %q, want %q", c.Path, repo)
	}
	if c.Name != "proj" {
		t.Errorf("Name = %q, want proj", c.Name)
	}
	// README.md + notes.txt are indexable; main.go is not.
	if c.DocCount != 2 {
		t.Errorf("DocCount = %d, want 2", c.DocCount)
	}
	if c.LastCommit.IsZero() {
		t.Error("LastCommit should not be zero")
	}
}

func TestDiscoverDocCountSkipsNodeModulesVendorAndSecrets(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "proj")
	initRepo(t, repo, "README.md")
	// Not part of the commit, but still on disk under the repo — doc
	// counting walks the filesystem, not git ls-files.
	write(t, filepath.Join(repo, "node_modules", "pkg", "readme.md"))
	write(t, filepath.Join(repo, "vendor", "lib", "readme.md"))
	write(t, filepath.Join(repo, "secrets", "notes.md"))
	write(t, filepath.Join(repo, ".hidden", "notes.md"))

	cands, err := Discover(context.Background(), Options{Roots: []string{root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", cands)
	}
	if cands[0].DocCount != 1 {
		t.Errorf("DocCount = %d, want 1 (only README.md)", cands[0].DocCount)
	}
}

func TestDiscoverMultipleReposSortedByLastCommitDesc(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	older := filepath.Join(root, "older")
	newer := filepath.Join(root, "newer")
	initRepo(t, older, "a.md")
	time.Sleep(1100 * time.Millisecond) // git commit time has 1s resolution
	initRepo(t, newer, "b.md")

	cands, err := Discover(context.Background(), Options{Roots: []string{root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %+v", cands)
	}
	if cands[0].Path != newer || cands[1].Path != older {
		t.Fatalf("expected newer first, got %+v", cands)
	}
}

func TestDiscoverCapsAtMaxCandidates(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		initRepo(t, filepath.Join(root, "repo"+string(rune('a'+i))), "a.md")
	}

	cands, err := Discover(context.Background(), Options{Roots: []string{root}, MaxCandidates: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates (capped), got %d", len(cands))
	}
}

func TestDiscoverNoReposReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "notes.md")) // not a repo

	cands, err := Discover(context.Background(), Options{Roots: []string{root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates, got %+v", cands)
	}
}

func TestDiscoverEmptyRootsReturnsEmpty(t *testing.T) {
	cands, err := Discover(context.Background(), Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates for empty roots, got %+v", cands)
	}
}

func TestDiscoverContextCancel(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "proj"), "a.md")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, Options{Roots: []string{root}}, nil); err == nil {
		t.Fatal("expected ctx cancellation error")
	}
}

func TestDiscoverProgressCallback(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "proj"), "a.md")

	var calls []int
	_, err := Discover(context.Background(), Options{Roots: []string{root}}, func(scanned int) {
		calls = append(calls, scanned)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("expected one progress call with 1, got %v", calls)
	}
}
