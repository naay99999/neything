package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/projects"
)

const cardHint = "Open an agent here; it will be asked to write one"

// projectChecks reports how many cards exist and, inside a repo, the state of
// that repo's card.
func projectChecks(ctx context.Context, cfg *config.Config, cwd string, now time.Time) []checkResult {
	n := 0
	if entries, err := os.ReadDir(projectsDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "index.md" {
				n++
			}
		}
	}
	res := []checkResult{{Check: "project_cards", OK: true, Message: fmt.Sprintf("Project cards: %d", n)}}
	root, ok := neycontext.ResolveRepo(ctx, cwd)
	if !ok {
		return res
	}
	reg, _ := projects.LoadRegistry(projectsDir())
	e, found := reg.Lookup(root, neycontext.RepoRemote(ctx, root))
	st, reason := projects.Missing, ""
	if found {
		st, reason, _ = cardState(ctx, cfg, e, now)
	}
	c := checkResult{Check: "project_card"}
	switch st {
	case projects.Fresh:
		c.OK, c.Message = true, "Project card: fresh"
	case projects.Stale:
		c.Message, c.Hint = "Project card: stale ("+reason+")", cardHint
	default:
		c.Message, c.Hint = "Project card: missing", cardHint
	}
	return append(res, c)
}
