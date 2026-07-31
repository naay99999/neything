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

// ScanRepos walks each root (depth capped, skipping node_modules, vendor,
// and dot-directories) looking for git repositories, and returns one
// Project per repo found, sorted by LastCommit descending.
//
// Any git error for a given repo (no commits yet, corrupt .git, etc.)
// causes that repo to be skipped silently — the scan itself never fails.
// If git is not on PATH, ScanRepos returns an empty slice without error.
func ScanRepos(ctx context.Context, roots []string) []Project {
	projects := []Project{}
	if _, err := exec.LookPath("git"); err != nil {
		return projects
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		scanDir(ctx, root, 0, &projects)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastCommit.After(projects[j].LastCommit)
	})
	return projects
}

// scanDir looks for a .git directory directly inside dir; if found it
// builds a Project and does not recurse further. Otherwise it recurses
// into subdirectories (skipping node_modules/vendor/dot-dirs) up to
// scanMaxDepth.
func scanDir(ctx context.Context, dir string, depth int, projects *[]Project) {
	if ctx.Err() != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, e := range entries {
		if e.IsDir() && e.Name() == ".git" {
			if p, ok := buildProject(ctx, dir); ok {
				*projects = append(*projects, p)
			}
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
		scanDir(ctx, filepath.Join(dir, name), depth+1, projects)
	}
}

// buildProject shells out to git to describe the repo at path. Any git
// failure (empty repo with no commits, detached/corrupt state, etc.)
// returns ok=false so the caller skips it.
func buildProject(ctx context.Context, path string) (Project, bool) {
	logOut, err := exec.CommandContext(ctx, "git", "-C", path, "log", "-1", "--format=%ct%x00%s").Output()
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

	branchOut, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return Project{}, false
	}

	statusOut, err := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain").Output()
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
