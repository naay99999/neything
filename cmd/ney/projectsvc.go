package main

import (
	"context"
	"sort"
	"time"

	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/handoff"
	"github.com/naay99999/neything/internal/projects"
)

// projectActiveWindow: repos seen or committed to within this window are
// listed even without a card.
const projectActiveWindow = 30 * 24 * time.Hour

func projectsDir() string { return projects.Dir(config.NeyDir()) }

func cardExists(name string) bool {
	_, err := projects.Load(projects.CardPath(projectsDir(), name))
	return err == nil
}

// hookProjects builds the projects part of the session-start brief. It never
// fails: every error degrades to less output.
func hookProjects(ctx context.Context, cfg *config.Config, root string, inRepo bool, now time.Time) handoff.Projects {
	if !cfg.Projects.Inject {
		return handoff.Projects{}
	}
	dir := projectsDir()
	reg, _ := projects.LoadRegistry(dir)
	var cur projects.Entry
	if inRepo {
		cur = reg.Upsert(projects.Entry{Path: root, Remote: neycontext.RepoRemote(ctx, root), LastSeen: now})
		_ = reg.Save(dir)
	}

	others := make([]projects.Entry, 0, len(reg.Repos))
	for _, e := range reg.Repos {
		if inRepo && e.Name == cur.Name {
			continue
		}
		if now.Sub(e.LastSeen) <= projectActiveWindow || cardExists(e.Name) {
			others = append(others, e)
		}
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].LastSeen.After(others[j].LastSeen) })
	p := handoff.Projects{Max: cfg.Projects.NamesInBrief}
	for _, e := range others {
		p.Names = append(p.Names, e.Name)
	}

	if inRepo {
		switch st, reason, _ := cardState(ctx, cfg, cur, now); st {
		case projects.Missing:
			p.CardState = "missing"
		case projects.Stale:
			p.CardState, p.StaleReason = "stale", reason
		}
	}
	return p
}

// cardState loads e's card and decides whether it is missing, stale or
// fresh. An unparseable card counts as missing; a git error as fresh.
func cardState(ctx context.Context, cfg *config.Config, e projects.Entry, now time.Time) (projects.State, string, *projects.Card) {
	c, err := projects.Load(projects.CardPath(projectsDir(), e.Name))
	if err != nil {
		return projects.Missing, "", nil
	}
	commits, changed, known, err := neycontext.CardDrift(ctx, e.Path, c.Commit, projects.KeyFiles)
	if err != nil {
		return projects.Fresh, "", &c
	}
	st, reason := projects.Staleness(c.Updated, commits, changed, known, now, cfg.Projects.StaleCommits, cfg.Projects.StaleDays)
	return st, reason, &c
}
