package neycontext

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ResolveRepo returns the main checkout root for dir (a linked worktree maps
// to its main checkout). ok=false when dir is not inside a git repo; root is
// then filepath.Clean(dir).
func ResolveRepo(ctx context.Context, dir string) (root string, ok bool) {
	fallback := filepath.Clean(dir)
	out, err := gitCmd(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return fallback, false
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return fallback, false
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), true
	}
	// Bare or otherwise unusual layout: fall back to the work tree top.
	out, err = gitCmd(ctx, dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fallback, false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return fallback, false
	}
	return top, true
}

// GitState is the live state of a repo shown on a brief's "Now:" line.
type GitState struct {
	Branch       string
	Uncommitted  int // lines of `git status --porcelain`
	CommitsSince int // commits on HEAD after Since
}

// RepoState reads the current branch, uncommitted-file count, and the number
// of commits on HEAD made after since. A zero since yields CommitsSince = 0.
func RepoState(ctx context.Context, root string, since time.Time) (GitState, error) {
	var st GitState
	// symbolic-ref works on an unborn branch; rev-parse covers detached HEAD
	// ("HEAD"). A branch lookup failure is not fatal.
	out, err := gitCmd(ctx, root, "symbolic-ref", "--short", "-q", "HEAD").Output()
	if err != nil {
		out, _ = gitCmd(ctx, root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	}
	st.Branch = strings.TrimSpace(string(out))

	out, err = gitCmd(ctx, root, "status", "--porcelain").Output()
	if err != nil {
		return st, fmt.Errorf("git status: %w", err)
	}
	if s := strings.TrimRight(string(out), "\n"); s != "" {
		st.Uncommitted = strings.Count(s, "\n") + 1
	}

	if since.IsZero() {
		return st, nil
	}
	// Unborn branch: no commits, rev-list would fail.
	if gitCmd(ctx, root, "rev-parse", "--verify", "-q", "HEAD").Run() != nil {
		return st, nil
	}
	out, err = gitCmd(ctx, root, "rev-list", "--count", "--since="+since.Format(time.RFC3339), "HEAD").Output()
	if err != nil {
		return st, fmt.Errorf("git rev-list: %w", err)
	}
	st.CommitsSince, err = strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return st, fmt.Errorf("git rev-list: %w", err)
	}
	return st, nil
}
