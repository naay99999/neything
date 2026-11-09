package handoff

import (
	"fmt"
	"strings"
	"testing"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/session"
)

func TestProjectsSectionCopy(t *testing.T) {
	names := []string{"neything", "portfolio"}
	cases := map[string]Projects{
		"## ney — projects\nYour projects (memory in ney; call get_project before guessing): neything, portfolio\nThis project has no ney card yet. Once you understand it, call update_project.\n":                                                                                    {Names: names, CardState: "missing"},
		"## ney — projects\nYour projects (memory in ney; call get_project before guessing): neything, portfolio\nThis project's ney card is out of date (42 commits since it was written; go.mod changed). If anything below changed, call update_project: summary, status, facts.\n": {Names: names, CardState: "stale", StaleReason: "42 commits since it was written; go.mod changed"},
		"## ney — projects\nYour projects (memory in ney; call get_project before guessing): neything, portfolio\n":                                                                                                                                                                    {Names: names},
		"## ney — projects\nThis project has no ney card yet. Once you understand it, call update_project.\n":                                                                                                                                                                          {CardState: "missing"},
	}
	for want, p := range cases {
		if got := projectsSection(p); got != want {
			t.Errorf("projectsSection(%+v) =\n%q\nwant\n%q", p, got, want)
		}
	}
	if got := projectsSection(Projects{}); got != "" {
		t.Errorf("empty Projects rendered %q", got)
	}
}

func TestProjectsSectionCap(t *testing.T) {
	var names []string
	for i := 0; i < 25; i++ {
		names = append(names, fmt.Sprintf("p%02d", i))
	}
	got := projectsSection(Projects{Names: names, Max: 20})
	if !strings.Contains(got, "p19 (+5 more)\n") || strings.Contains(got, "p20") {
		t.Errorf("cap 20 of 25:\n%s", got)
	}
}

func TestInjectBudgetWithManyProjects(t *testing.T) {
	s := baseSession()
	huge := strings.Repeat("word ", 4000)
	s.FirstPrompt, s.LastPrompt, s.LastMessage = huge, huge, huge
	for i := 0; i < 200; i++ {
		s.Files = append(s.Files, session.FileChange{Path: fmt.Sprintf("pkg/some/deep/dir/file%03d.go", i), Ops: []string{"edit"}, Count: 2})
	}
	b := Build("repo", []session.Session{s, baseSession()}, nil, neycontext.GitState{Branch: "x"}, true, testNow)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("project-with-a-long-name-%04d", i))
	}
	b.AddProjects(Projects{Names: names, CardState: "stale", StaleReason: strings.Repeat("x", 100)})
	out := b.RenderInject()
	i := strings.Index(out, "## ney — projects\n")
	if len(out) > 3072 || i < 0 || len(out)-i > 600 {
		t.Fatalf("len=%d section at %d (section %d bytes)", len(out), i, len(out)-i)
	}
	if !strings.HasPrefix(out, "## ney — unfinished work in repo\n") || !strings.Contains(out, "more)") ||
		!strings.Contains(out, "out of date") {
		t.Errorf("lost handoff heading, (+N more) or the card line:\n%s", out)
	}
}

func TestProjectsOnlyBrief(t *testing.T) {
	b := Build("repo", nil, nil, neycontext.GitState{}, false, testNow)
	if !b.Empty() {
		t.Fatal("brief without sessions should start empty")
	}
	b.AddProjects(Projects{Names: []string{"neything"}})
	out := b.RenderInject()
	if b.Empty() || !strings.HasPrefix(out, "## ney — projects\n") || strings.Contains(out, "unfinished work") {
		t.Fatalf("projects-only brief:\n%s", out)
	}
	if b.RenderFull() != "" {
		t.Errorf("RenderFull should stay empty without sessions")
	}
}

func TestProjectsNamesScrubbed(t *testing.T) {
	var b Brief
	b.AddProjects(Projects{Names: []string{"#evil\nline", "ok"}, CardState: "stale", StaleReason: "x\n## forged"})
	out := b.RenderInject()
	if !strings.Contains(out, "): evil line, ok\n") || strings.Contains(out, "\n## forged") {
		t.Errorf("not flattened:\n%s", out)
	}
}
