package handoff

import (
	"fmt"
	"strings"
)

// projectsCap bounds the projects section inside the inject brief.
const projectsCap = 600

const (
	projectsHeading = "## ney — projects\n"
	projectsLead    = "Your projects (memory in ney; call get_project before guessing): "
	cardMissingLine = "This project has no ney card yet. Once you understand it, call update_project.\n"
	cardStaleLine   = "This project's ney card is out of date (%s). If anything below changed, call update_project: summary, status, facts.\n"
	staleReasonMax  = 200
)

// Projects is the projects part of a session-start brief.
type Projects struct {
	Names       []string // newest first, without the current repo
	CardState   string   // "", "missing", "stale"
	StaleReason string
	Max         int // names cap (projects.names_in_brief); 0 = no cap
}

// AddProjects attaches the projects part, scrubbed like every other brief
// string.
func (b *Brief) AddProjects(p Projects) {
	names := make([]string, 0, len(p.Names))
	for _, n := range p.Names {
		if n = inline(scrub(n)); n != "" {
			names = append(names, n)
		}
	}
	p.Names = names
	p.StaleReason = scrub(p.StaleReason)
	b.Projects = p
}

func (p Projects) empty() bool { return len(p.Names) == 0 && p.CardState == "" }

// projectsSection renders the section, at most projectsCap bytes: names past
// Max, then past the byte budget, collapse into "(+N more)".
func projectsSection(p Projects) string {
	if p.empty() {
		return ""
	}
	card := ""
	switch p.CardState {
	case "missing":
		card = cardMissingLine
	case "stale":
		card = fmt.Sprintf(cardStaleLine, clipRunes(inline(p.StaleReason), staleReasonMax))
	}
	names := p.Names
	if p.Max > 0 && len(names) > p.Max {
		names = names[:p.Max]
	}
	for keep := len(names); ; keep-- {
		var sb strings.Builder
		sb.WriteString(projectsHeading)
		if keep > 0 {
			sb.WriteString(projectsLead + strings.Join(names[:keep], ", "))
			if more := len(p.Names) - keep; more > 0 {
				fmt.Fprintf(&sb, " (+%d more)", more)
			}
			sb.WriteString("\n")
		}
		sb.WriteString(card)
		if sb.Len() <= projectsCap || keep == 0 {
			return trimBytes(sb.String(), projectsCap)
		}
	}
}
