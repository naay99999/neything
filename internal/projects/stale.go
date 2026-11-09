package projects

import (
	"fmt"
	"strings"
	"time"
)

// KeyFiles are the files whose change since a card was written marks it stale.
var KeyFiles = []string{":(glob)README*", "CLAUDE.md", "AGENTS.md", "package.json", "go.mod", "pyproject.toml", "Cargo.toml"}

// State is a card's freshness for the current repo.
type State int

const (
	Fresh State = iota
	Missing
	Stale
)

// Staleness decides whether a card written at updated is stale, and why.
// commits/changed come from CardDrift; known=false (the card's commit is not
// in the repo) leaves only the age rule.
func Staleness(updated time.Time, commits int, changed []string, known bool, now time.Time, staleCommits, staleDays int) (State, string) {
	var parts []string
	if known && staleCommits > 0 && commits >= staleCommits {
		parts = append(parts, fmt.Sprintf("%d commits since it was written", commits))
	}
	if known && len(changed) > 0 {
		parts = append(parts, strings.Join(changed, ", ")+" changed")
	}
	if staleDays > 0 && !updated.IsZero() {
		if days := int(now.Sub(updated) / (24 * time.Hour)); days >= staleDays {
			parts = append(parts, fmt.Sprintf("written %d days ago", days))
		}
	}
	if len(parts) == 0 {
		return Fresh, ""
	}
	return Stale, strings.Join(parts, "; ")
}
