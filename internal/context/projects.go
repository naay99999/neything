// Package neycontext assembles the "layered context" ney serves to AI
// clients: a small Layer-1 bootstrap blob (profile + active projects +
// how-to-dig-deeper) rendered fresh on every get_context call, plus the
// write paths (remember, update_profile) that keep the underlying files
// current.
//
// The package takes every input as a parameter (roots, paths, time
// windows) and never imports internal/config — callers own wiring config
// into these functions. Layer 1 must never fail: functions here degrade
// (skip a broken repo, fall back to an empty list) rather than return an
// error, except where a real filesystem error makes progress impossible.
package neycontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Project describes one git repository discovered under a dev root, or
// mapped from an indexed workspace.
type Project struct {
	Name              string
	Path              string
	Branch            string
	LastCommitSubject string
	LastCommit        time.Time
	Dirty             bool
	Indexed           bool // set by the caller (e.g. against served workspace roots); ScanRepos never sets this
}

// scanMaxDepth caps how many directory levels ScanRepos descends below each
// root before giving up on that branch.
const scanMaxDepth = 4

// skipDirNames are directory basenames ScanRepos never descends into, even
// within the depth cap.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
}

// scanConcurrency bounds how many repositories are described at once.
// Describing one repo costs three git processes, and a scan of a normal dev
// root covers dozens — done serially that is the whole cost of get_context.
// The work is dominated by process spawn and filesystem I/O, so a modest
// fixed width beats scaling with CPU count.
const scanConcurrency = 8

// ScanRepos walks each root (depth capped, skipping node_modules, vendor,
// and dot-directories) looking for git repositories, and returns one
// Project per repo found, sorted by LastCommit descending.
//
// Discovery (cheap, os.ReadDir) is separated from description (three git
// forks per repo) so the latter can run concurrently. Results are written
// into a pre-sized slice by index rather than appended from the workers, so
// the order reaching the sort is identical on every call.
//
// Any git error for a given repo (no commits yet, corrupt .git, etc.)
// causes that repo to be skipped silently — the scan itself never fails.
// If git is not on PATH, ScanRepos returns an empty slice without error.
func ScanRepos(ctx context.Context, roots []string) []Project {
	projects := []Project{}
	if _, err := exec.LookPath("git"); err != nil {
		return projects
	}

	var dirs []string
	for _, root := range roots {
		collectRepoDirs(ctx, filepath.Clean(root), 0, &dirs)
	}
	if len(dirs) == 0 {
		return projects
	}

	found := make([]Project, len(dirs))
	ok := make([]bool, len(dirs))

	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func(i int, dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			found[i], ok[i] = buildProject(ctx, dir)
		}(i, dir)
	}
	wg.Wait()

	for i := range dirs {
		if ok[i] {
			projects = append(projects, found[i])
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastCommit.After(projects[j].LastCommit)
	})
	return projects
}

// collectRepoDirs appends the path of every git repository found under dir to
// out. A directory containing .git is a repo and is never descended into;
// anything else is recursed into (skipping node_modules/vendor/dot-dirs) up
// to scanMaxDepth. Pure filesystem work — no git process is started here.
func collectRepoDirs(ctx context.Context, dir string, depth int, out *[]string) {
	if ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() && e.Name() == ".git" {
			*out = append(*out, dir)
			return // never descend into a repo
		}
	}

	if depth >= scanMaxDepth {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || skipDirNames[name] {
			continue
		}
		collectRepoDirs(ctx, filepath.Join(dir, name), depth+1, out)
	}
}

// gitCmd builds a git invocation against a repository ney did not create.
//
// ScanRepos describes whatever repositories it finds under the configured dev
// roots, and a repository carries its own configuration. git honours several
// keys that name a command to run — core.fsmonitor is the notable one — so
// plain `git status` inside a repo whose .git/config an attacker controls
// (unpacked from an archive, say) executes that command. The -c overrides
// below neutralise those keys for this process only; they do not touch the
// user's own git config.
//
// GIT_OPTIONAL_LOCKS=0 is not about safety: it stops `git status` from taking
// index.lock and rewriting .git/index behind the user's back, on every single
// get_context call.
func gitCmd(ctx context.Context, repo string, args ...string) *exec.Cmd {
	full := append([]string{
		"-C", repo,
		"-c", "core.fsmonitor=",
		"-c", "core.hooksPath=/dev/null",
		"-c", "protocol.ext.allow=never",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

// buildProject shells out to git to describe the repo at path. Any git
// failure (empty repo with no commits, detached/corrupt state, etc.)
// returns ok=false so the caller skips it.
func buildProject(ctx context.Context, path string) (Project, bool) {
	logOut, err := gitCmd(ctx, path, "log", "-1", "--format=%ct%x00%s").Output()
	if err != nil {
		return Project{}, false
	}
	parts := strings.SplitN(strings.TrimRight(string(logOut), "\n"), "\x00", 2)
	if len(parts) != 2 {
		return Project{}, false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return Project{}, false
	}

	branchOut, err := gitCmd(ctx, path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return Project{}, false
	}

	statusOut, err := gitCmd(ctx, path, "status", "--porcelain").Output()
	if err != nil {
		return Project{}, false
	}

	return Project{
		Name:              filepath.Base(path),
		Path:              path,
		Branch:            strings.TrimSpace(string(branchOut)),
		LastCommitSubject: parts[1],
		LastCommit:        time.Unix(sec, 0),
		Dirty:             strings.TrimSpace(string(statusOut)) != "",
	}, true
}
