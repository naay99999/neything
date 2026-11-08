package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/handoff"
	"github.com/naay99999/neything/internal/session"
	"github.com/naay99999/neything/internal/session/claudecode"
	"github.com/naay99999/neything/internal/session/codex"
)

const (
	handoffMaxSessions = 3
	handoffGitTimeout  = 2 * time.Second
	defaultMaxAgeDays  = 14
)

// handoffRequest is what every surface (CLI, hook, MCP) asks for.
type handoffRequest struct {
	Cwd              string
	ExcludeSessionID string
	SessionID        string // when set, that session is primary
	Now              time.Time
}

// errSessionNotFound is returned by buildBrief when req.SessionID matches no
// session in the recent window.
type errSessionNotFound struct{ ID, Repo string }

func (e errSessionNotFound) Error() string {
	return fmt.Sprintf("session %s not found for %s", e.ID, e.Repo)
}

// sourcesFor returns the transcript sources enabled in cfg. The home dir is
// resolved at call time (tests set HOME).
func sourcesFor(cfg *config.Config) []session.Source {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	var out []session.Source
	for _, name := range cfg.Sources {
		switch name {
		case "claude-code":
			if home != "" {
				out = append(out, claudecode.New(filepath.Join(home, ".claude", "projects")))
			}
		case "codex":
			if h, ok := codex.Home(); ok {
				out = append(out, codex.New(h))
			}
		}
	}
	return out
}

// recentRefs merges every source's refs for root, newest first. A failing
// source is skipped.
func recentRefs(cfg *config.Config, root string, now time.Time) []session.Ref {
	days := cfg.Handoff.MaxAgeDays
	if days <= 0 {
		days = defaultMaxAgeDays
	}
	since := now.AddDate(0, 0, -days)
	var refs []session.Ref
	for _, src := range sourcesFor(cfg) {
		rs, err := src.Recent(root, since)
		if err != nil {
			continue
		}
		refs = append(refs, rs...)
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Modified.After(refs[j].Modified) })
	return refs
}

func sourceFor(cfg *config.Config, agent string) session.Source {
	for _, src := range sourcesFor(cfg) {
		if src.Name() == agent {
			return src
		}
	}
	return nil
}

// loadSessions loads refs in order until limit sessions are loaded; the first
// is loaded with full. Refs that are not sessions or fail to load are skipped.
func loadSessions(cfg *config.Config, refs []session.Ref, limit int, full bool) []session.Session {
	var out []session.Session
	for _, ref := range refs {
		if len(out) >= limit {
			break
		}
		src := sourceFor(cfg, ref.Agent)
		if src == nil {
			continue
		}
		s, err := src.Load(ref, full && len(out) == 0)
		if err != nil {
			if !errors.Is(err, session.ErrNotASession) {
				fmt.Fprintf(os.Stderr, "handoff: load %s: %v\n", ref.ID, err)
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// relativizeFiles rewrites FileChange paths inside root to be repo-relative;
// absolute paths would eat the inject budget.
func relativizeFiles(s *session.Session, root string) {
	if len(s.Files) == 0 {
		return
	}
	files := make([]session.FileChange, len(s.Files))
	copy(files, s.Files)
	for i := range files {
		rel, err := filepath.Rel(root, files[i].Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		files[i].Path = rel
	}
	s.Files = files
}

// buildBrief is the one place every surface builds a handoff brief.
func buildBrief(ctx context.Context, cfg *config.Config, req handoffRequest, full bool) (handoff.Brief, string, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
	defer cancel()

	root, inGit := neycontext.ResolveRepo(gitCtx, req.Cwd)
	repoName := filepath.Base(root)

	var refs, rest []session.Ref
	var chosen *session.Ref
	for _, r := range recentRefs(cfg, root, now) {
		if r.ID == req.ExcludeSessionID && req.ExcludeSessionID != "" {
			continue
		}
		if req.SessionID != "" && r.ID == req.SessionID && chosen == nil {
			r := r
			chosen = &r
			continue
		}
		rest = append(rest, r)
	}
	if req.SessionID != "" {
		if chosen == nil {
			return handoff.Brief{}, root, errSessionNotFound{ID: req.SessionID, Repo: repoName}
		}
		refs = append(refs, *chosen)
	}
	refs = append(refs, rest...)

	sessions := loadSessions(cfg, refs, handoffMaxSessions, full)
	// The requested session may exist but fail to load (or not be a real
	// session); never let another session silently become primary.
	if req.SessionID != "" && (len(sessions) == 0 || sessions[0].ID != req.SessionID) {
		return handoff.Brief{}, root, errSessionNotFound{ID: req.SessionID, Repo: repoName}
	}
	for i := range sessions {
		relativizeFiles(&sessions[i], root)
	}

	notes, _ := handoff.ReadNotes(config.NeyDir(), root)

	var git neycontext.GitState
	gitOK := false
	if inGit {
		var since time.Time
		if len(sessions) > 0 {
			since = sessions[0].End
		}
		if st, err := neycontext.RepoState(gitCtx, root, since); err == nil {
			git, gitOK = st, true
		}
	}
	return handoff.Build(repoName, sessions, notes, git, gitOK, now), root, nil
}

// listSessions returns up to limit recent sessions for the repo containing
// cwd, newest first, loaded without full tails.
func listSessions(ctx context.Context, cfg *config.Config, cwd string, limit int) ([]session.Session, string, error) {
	gitCtx, cancel := context.WithTimeout(ctx, handoffGitTimeout)
	defer cancel()
	root, _ := neycontext.ResolveRepo(gitCtx, cwd)
	sessions := loadSessions(cfg, recentRefs(cfg, root, time.Now()), limit, false)
	for i := range sessions {
		relativizeFiles(&sessions[i], root)
	}
	return sessions, root, nil
}
