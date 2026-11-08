package neycontext

import (
	"fmt"
	"strings"
	"time"
)

// diggingDeeper is the static "how to go further" section appended to
// every Layer-1 render. It names the write tools; get_context
// itself is deliberately not listed.
const diggingDeeper = `## Digging deeper
- remember: save a new fact or decision to memory
- update_profile: propose an edit to a profile section`

// Render assembles the Layer-1 context blob: profile, an active-projects
// list windowed to the last activeDays and capped at maxShown entries (with
// a remainder count for the rest), and the static digging-deeper section.
// It is a pure function and never errors — a blank profile or empty
// project list just renders a smaller (but still valid) blob.
//
// now anchors "active" and the relative-time strings ("2h ago") so callers
// (and tests) get deterministic output; production callers pass
// time.Now().
func Render(profile string, projects []Project, activeDays, maxShown int, now time.Time) string {
	var b strings.Builder

	b.WriteString("## Who you're working with\n")
	p := strings.TrimSpace(profile)
	if p == "" {
		p = "(no profile yet — run update_profile to add one)"
	}
	b.WriteString(p)
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "## Active projects (git activity, last %d days)\n", activeDays)
	b.WriteString(renderActiveProjects(projects, activeDays, maxShown, now))
	b.WriteString("\n\n")

	b.WriteString(diggingDeeper)
	b.WriteString("\n")

	return b.String()
}

func renderActiveProjects(projects []Project, activeDays, maxShown int, now time.Time) string {
	if maxShown < 0 {
		maxShown = 0
	}
	window := time.Duration(activeDays) * 24 * time.Hour
	if activeDays < 0 {
		window = 0
	}

	active := make([]Project, 0, len(projects))
	for _, proj := range projects {
		if proj.LastCommit.IsZero() {
			continue
		}
		age := now.Sub(proj.LastCommit)
		if age < 0 {
			age = 0
		}
		if age <= window {
			active = append(active, proj)
		}
	}

	if len(active) == 0 {
		return "(none in this window)"
	}

	shown := active
	remainder := 0
	if len(active) > maxShown {
		shown = active[:maxShown]
		remainder = len(active) - maxShown
	}

	var lines []string
	for _, proj := range shown {
		lines = append(lines, renderProjectLine(proj, now))
	}
	if remainder > 0 {
		lines = append(lines, fmt.Sprintf("- +%d more not shown", remainder))
	}
	return strings.Join(lines, "\n")
}

func renderProjectLine(p Project, now time.Time) string {
	parts := []string{fmt.Sprintf("%s — %s", p.Name, p.Path)}
	if p.Branch != "" {
		parts = append(parts, p.Branch)
	}

	timePart := RelativeTime(p.LastCommit, now)
	if p.LastCommitSubject != "" {
		timePart = fmt.Sprintf("%s: %q", timePart, p.LastCommitSubject)
	}
	parts = append(parts, timePart)

	return "- " + strings.Join(parts, " · ")
}

// RelativeTime renders t relative to now as a short human string ("2h
// ago", "3d ago"). A zero t renders as "unknown".
func RelativeTime(t, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}
