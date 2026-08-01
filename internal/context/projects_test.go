package neycontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the test if git isn't on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a git repo at dir with one commit containing a file
// named "README.md" with the given subject as the commit message.
func initRepo(t *testing.T, dir, subject string) {
	t.Helper()
	requireGit(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", subject)
}

func TestScanRepos_FindsFixtureRepo(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	initRepo(t, repoDir, "initial commit")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d: %+v", len(projects), projects)
	}
	p := projects[0]
	if p.Name != "myrepo" {
		t.Errorf("Name = %q, want myrepo", p.Name)
	}
	if p.Path != repoDir {
		t.Errorf("Path = %q, want %q", p.Path, repoDir)
	}
	if p.LastCommitSubject != "initial commit" {
		t.Errorf("LastCommitSubject = %q", p.LastCommitSubject)
	}
	if p.Branch == "" {
		t.Errorf("Branch is empty")
	}
	if p.Dirty {
		t.Errorf("Dirty = true, want false (nothing uncommitted)")
	}
	if p.LastCommit.IsZero() {
		t.Errorf("LastCommit is zero")
	}
	if p.Indexed {
		t.Errorf("Indexed = true, want false (ScanRepos never sets it)")
	}
}

func TestScanRepos_DirtyRepo(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	repoDir := filepath.Join(root, "dirty")
	initRepo(t, repoDir, "initial")
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if !projects[0].Dirty {
		t.Errorf("Dirty = false, want true (untracked file present)")
	}
}

func TestScanRepos_DoesNotDescendIntoRepo(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	outer := filepath.Join(root, "outer")
	initRepo(t, outer, "outer commit")
	// A nested ".git"-looking dir inside the repo must not be picked up
	// as a second project.
	nestedRepo := filepath.Join(outer, "nested")
	initRepo(t, nestedRepo, "nested commit")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project (no descending into repos), got %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "outer" {
		t.Errorf("expected outer repo only, got %q", projects[0].Name)
	}
}

func TestScanRepos_BrokenRepoSkipped(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	broken := filepath.Join(root, "broken")
	if err := os.MkdirAll(filepath.Join(broken, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// .git exists but is an empty directory, not a real git repo.

	good := filepath.Join(root, "good")
	initRepo(t, good, "good commit")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project (broken repo skipped), got %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "good" {
		t.Errorf("expected good repo, got %q", projects[0].Name)
	}
}

func TestScanRepos_SkipsNodeModulesVendorAndDotDirs(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "node_modules", "pkg"), "should be skipped")
	initRepo(t, filepath.Join(root, "vendor", "pkg"), "should be skipped")
	initRepo(t, filepath.Join(root, ".hidden", "pkg"), "should be skipped")
	initRepo(t, filepath.Join(root, "real"), "kept")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "real" {
		t.Errorf("expected real repo only, got %q", projects[0].Name)
	}
}

func TestScanRepos_DepthCapHonored(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	// Within cap: root/a/b/c/within (depth 4).
	within := filepath.Join(root, "a", "b", "c", "within")
	initRepo(t, within, "within cap")

	// Beyond cap: root/a/b/c/d/beyond (depth 5).
	beyond := filepath.Join(root, "a", "b", "c", "d", "beyond")
	initRepo(t, beyond, "beyond cap")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 1 {
		t.Fatalf("expected 1 project within depth cap, got %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "within" {
		t.Errorf("expected 'within' repo, got %q", projects[0].Name)
	}
}

func TestScanRepos_SortedByLastCommitDesc(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	older := filepath.Join(root, "older")
	initRepo(t, older, "older commit")
	// Force distinguishable commit times via GIT_AUTHOR/COMMITTER_DATE.
	setCommitDate(t, older, "2020-01-01T00:00:00")

	newer := filepath.Join(root, "newer")
	initRepo(t, newer, "newer commit")
	setCommitDate(t, newer, "2030-01-01T00:00:00")

	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "newer" || projects[1].Name != "older" {
		t.Errorf("expected [newer, older], got [%s, %s]", projects[0].Name, projects[1].Name)
	}
}

// setCommitDate amends the last commit's date in dir to the given
// RFC3339-ish local timestamp, so tests can control ordering deterministically.
func setCommitDate(t *testing.T, dir, date string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "commit", "--amend", "--no-edit", "--date", date)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit --amend: %v\n%s", err, out)
	}
}

func TestScanRepos_NoGitOnPath(t *testing.T) {
	// Simulate "no git on PATH" by pointing PATH somewhere empty.
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	root := t.TempDir()
	projects := ScanRepos(context.Background(), []string{root})
	if len(projects) != 0 {
		t.Fatalf("expected empty slice with no git on PATH, got %d", len(projects))
	}
}

func TestScanRepos_EmptyRoots(t *testing.T) {
	projects := ScanRepos(context.Background(), nil)
	if len(projects) != 0 {
		t.Fatalf("expected empty slice for nil roots, got %d", len(projects))
	}
}

func TestScanRepos_NonexistentRoot(t *testing.T) {
	requireGit(t)
	projects := ScanRepos(context.Background(), []string{filepath.Join(t.TempDir(), "does-not-exist")})
	if len(projects) != 0 {
		t.Fatalf("expected empty slice for nonexistent root, got %d", len(projects))
	}
}

// TestScanReposHardensGitInvocation: ScanRepos describes repositories ney did
// not create, and a repository carries its own config. git honours several
// keys that name a command to run, so a repo whose .git/config an attacker
// controls could otherwise execute it. core.fsmonitor is the reachable one
// here — `git status` runs it.
func TestScanReposHardensGitInvocation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "hostile")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	// The payload: a hook command git would run during `status`.
	canary := filepath.Join(root, "PWNED")
	run("config", "core.fsmonitor", "touch "+canary+" && false")

	projects := ScanRepos(context.Background(), []string{root})

	if _, err := os.Stat(canary); err == nil {
		t.Fatal("core.fsmonitor was executed — git invocations are not hardened")
	}
	if len(projects) != 1 {
		t.Fatalf("expected the repo to still be described, got %d projects", len(projects))
	}
	if projects[0].Name != "hostile" {
		t.Fatalf("unexpected project %+v", projects[0])
	}
}

// TestScanReposIsDeterministic: describing repos concurrently must not make
// the result order depend on which git process finished first.
func TestScanReposIsDeterministic(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		repo := filepath.Join(root, name)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
				// Identical commit timestamps, so the sort cannot break ties
				// and any instability comes from the scan itself.
				"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
		}
		run("init")
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-m", "initial")
	}

	first := ScanRepos(context.Background(), []string{root})
	if len(first) != 5 {
		t.Fatalf("expected 5 repos, got %d", len(first))
	}
	for i := 0; i < 5; i++ {
		got := ScanRepos(context.Background(), []string{root})
		if len(got) != len(first) {
			t.Fatalf("run %d returned %d repos, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].Path != first[j].Path {
				t.Fatalf("run %d position %d = %s, want %s — order is not deterministic",
					i, j, got[j].Path, first[j].Path)
			}
		}
	}
}
