package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
		// The remote is only needed to re-link a new or moved repo; a known path
		// skips that git call (the hook's budget is two).
		e := projects.Entry{Path: root, LastSeen: now}
		if _, known := reg.Lookup(root, ""); !known {
			e.Remote = neycontext.RepoRemote(ctx, root)
		}
		if saved, err := projects.UpdateRegistry(dir, hookLockTimeout, func(r *projects.Registry) {
			cur = upsertRepo(r, dir, e)
		}); err == nil {
			reg = saved
		} else {
			cur = upsertRepo(&reg, dir, e) // locked or unwritable: use it for this session only
		}
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

// hookLockTimeout bounds how long the hook waits for the registry lock; past
// it the hook proceeds without saving.
const hookLockTimeout = 100 * time.Millisecond

// mcpLockTimeout is the registry lock wait for MCP and CLI writers.
const mcpLockTimeout = 2 * time.Second

// upsertRepo records e. A repo new to the registry is matched against the
// cards on disk, so it adopts its own card and never takes another repo's
// card name (the registry may have been rebuilt after corruption).
func upsertRepo(r *projects.Registry, dir string, e projects.Entry) projects.Entry {
	if _, ok := r.Lookup(e.Path, e.Remote); ok {
		return r.Upsert(e)
	}
	return r.UpsertKnowing(e, projects.LoadCards(dir))
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

const profileHeadlineMax = 300

// profileHeadline is the start of profile.md as one line, for list_projects.
func profileHeadline() string {
	content, _, err := neycontext.LoadProfile(filepath.Join(config.NeyDir(), "profile.md"))
	var parts []string
	if err == nil {
		for _, l := range strings.Split(content, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "<!--") {
				continue
			}
			parts = append(parts, l)
		}
	}
	h := strings.Join(parts, " ")
	if h == "" {
		return "(no profile yet — use update_profile)"
	}
	if utf8.RuneCountInString(h) > profileHeadlineMax {
		h = string([]rune(h)[:profileHeadlineMax-1]) + "…"
	}
	return h
}

// registryItems turns registry entries into table-of-contents rows, newest
// activity first.
func registryItems(reg projects.Registry) []projects.Item {
	items := make([]projects.Item, 0, len(reg.Repos))
	for _, e := range reg.Repos {
		it := projects.Item{Name: e.Name, Path: e.Path, Activity: e.LastSeen}
		if c, err := projects.Load(projects.CardPath(projectsDir(), e.Name)); err == nil {
			it.HasCard, it.Summary = true, c.Summary
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Activity.After(items[j].Activity) })
	return items
}

// collectItems merges the registry with a dev_roots scan (repos committed to
// within the active window are registered), saves the registry, rewrites
// index.md, and returns the rows.
func collectItems(ctx context.Context, cfg *config.Config, scan []neycontext.Project, now time.Time) []projects.Item {
	dir := projectsDir()
	update := func(r *projects.Registry) {
		for _, p := range scan {
			if p.LastCommit.IsZero() || now.Sub(p.LastCommit) > projectActiveWindow ||
				hookRepoDisabled(cfg.Handoff.DisabledRepos, p.Path, p.Path) {
				continue
			}
			e := projects.Entry{Path: p.Path, LastSeen: p.LastCommit}
			if _, known := r.Lookup(p.Path, ""); !known {
				e.Remote = neycontext.RepoRemote(ctx, p.Path) // re-link moved repos
			}
			upsertRepo(r, dir, e)
		}
	}
	reg, err := projects.UpdateRegistry(dir, mcpLockTimeout, update)
	if err != nil { // locked or unwritable: show the merged view without saving
		reg, _ = projects.LoadRegistry(dir)
		update(&reg)
	}
	items := registryItems(reg)
	_ = projects.WriteIndex(dir, items)
	return items
}

func renderProjectList(headline string, items []projects.Item, now time.Time) string {
	var b strings.Builder
	b.WriteString("## Who you're working with\n" + headline + "\n\n## Projects\n")
	if len(items) == 0 {
		b.WriteString("(none yet — projects appear once an agent session runs in them)\n")
	}
	for _, it := range items {
		b.WriteString("- " + projects.TOCLine(it, now) + "\n")
	}
	return b.String()
}

// renderProject is get_project's text: the card plus one live git line.
func renderProject(ctx context.Context, query string, now time.Time) (string, error) {
	reg, _ := projects.LoadRegistry(projectsDir())
	e, ok := reg.Find(query)
	if !ok {
		names := make([]string, 0, len(reg.Repos))
		for _, r := range reg.Repos {
			names = append(names, r.Name)
		}
		msg := fmt.Sprintf("no project named %q", query)
		if c := projects.Closest(names, query, 3); len(c) > 0 {
			msg += "; closest: " + strings.Join(c, ", ")
		}
		return "", errors.New(msg)
	}
	var b strings.Builder
	c, err := projects.Load(projects.CardPath(projectsDir(), e.Name))
	if err != nil {
		fmt.Fprintf(&b, "%s · %s — no card yet. Read its README.md / CLAUDE.md for details.\n", e.Name, e.Path)
	} else {
		text := string(c.Marshal())
		text = strings.TrimPrefix(text, "---\n")
		text = strings.Replace(text, "\n---\n", "\n\n", 1)
		b.WriteString(text)
	}
	if st, err := neycontext.RepoState(ctx, e.Path, time.Time{}); err == nil {
		live := "Live: branch " + st.Branch
		if subj, at, err := neycontext.LastCommit(ctx, e.Path); err == nil {
			live += fmt.Sprintf(" · last commit %s %q", neycontext.RelativeTime(at, now), subj)
		}
		live += fmt.Sprintf(" · %d uncommitted", st.Uncommitted)
		b.WriteString("\n" + live + "\n")
	}
	return b.String(), nil
}

// updateProjectCard applies p to the card of the repo containing cwd.
func updateProjectCard(ctx context.Context, cwd, agent string, p projects.Patch, now time.Time) (string, error) {
	root, ok := neycontext.ResolveRepo(ctx, cwd)
	if !ok {
		return "", errors.New("cwd is not inside a git repository")
	}
	dir := projectsDir()
	remote := neycontext.RepoRemote(ctx, root)
	var e projects.Entry
	reg, err := projects.UpdateRegistry(dir, mcpLockTimeout, func(r *projects.Registry) {
		e = upsertRepo(r, dir, projects.Entry{Path: root, Remote: remote, LastSeen: now})
	})
	if err != nil {
		return "", fmt.Errorf("project registry: %w", err)
	}
	path := projects.CardPath(dir, e.Name)
	c, err := projects.Load(path)
	if err != nil {
		c = projects.Card{} // missing, or unparseable: Save backs that up first
	}
	if err := c.Apply(p); err != nil {
		return "", err
	}
	c.Name, c.Path, c.Remote = e.Name, root, remote
	c.Commit, c.Updated, c.UpdatedBy = neycontext.HeadCommit(ctx, root), now.UTC().Truncate(time.Second), agent
	if err := projects.Save(path, c); err != nil {
		return "", err
	}
	_ = projects.WriteIndex(dir, registryItems(reg))
	return renderProject(ctx, e.Name, now)
}
