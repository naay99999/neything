package neycontext

import (
	"strings"
	"testing"
	"time"
)

func TestRender_EmptyInputsNeverErrors(t *testing.T) {
	out := Render("", nil, 14, 10, time.Now())
	if out == "" {
		t.Fatalf("Render returned empty string for empty inputs")
	}
	if !strings.Contains(out, "## Who you're working with") {
		t.Errorf("missing profile header:\n%s", out)
	}
	if !strings.Contains(out, "## Active projects") {
		t.Errorf("missing active projects header:\n%s", out)
	}
	if !strings.Contains(out, "## Digging deeper") {
		t.Errorf("missing digging deeper section:\n%s", out)
	}
	if !strings.Contains(out, "no profile yet") {
		t.Errorf("expected placeholder note for blank profile:\n%s", out)
	}
}

func TestRender_ContainsProjectLine(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	projects := []Project{
		{
			Name:              "neything",
			Path:              "/home/user/workspace/neything",
			Branch:            "main",
			LastCommitSubject: "Update README",
			LastCommit:        now.Add(-2 * time.Hour),
			Indexed:           true,
		},
	}

	out := Render("Jane, engineer", projects, 14, 10, now)
	if !strings.Contains(out, "neything") {
		t.Errorf("project name missing:\n%s", out)
	}
	if !strings.Contains(out, "2h ago") {
		t.Errorf("expected relative time '2h ago':\n%s", out)
	}
	if !strings.Contains(out, "Update README") {
		t.Errorf("expected commit subject:\n%s", out)
	}
	if !strings.Contains(out, "indexed") {
		t.Errorf("expected indexed marker:\n%s", out)
	}
	if !strings.Contains(out, "Jane, engineer") {
		t.Errorf("profile content missing:\n%s", out)
	}
}

func TestRender_DaysAgoFormatting(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	projects := []Project{
		{Name: "foo", Path: "/p/foo", Branch: "feat/x", LastCommit: now.Add(-3 * 24 * time.Hour)},
	}
	out := Render("", projects, 14, 10, now)
	if !strings.Contains(out, "3d ago") {
		t.Errorf("expected '3d ago':\n%s", out)
	}
	if !strings.Contains(out, "not indexed") {
		t.Errorf("expected not-indexed marker:\n%s", out)
	}
}

func TestRender_ActiveDaysWindowFilter(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	projects := []Project{
		{Name: "recent", Path: "/p/recent", LastCommit: now.Add(-1 * 24 * time.Hour)},
		{Name: "stale", Path: "/p/stale", LastCommit: now.Add(-30 * 24 * time.Hour)},
	}
	out := Render("", projects, 14, 10, now)
	if !strings.Contains(out, "recent") {
		t.Errorf("recent project should be shown:\n%s", out)
	}
	if strings.Contains(out, "stale") {
		t.Errorf("stale project outside window should not be shown:\n%s", out)
	}
}

func TestRender_MaxShownCapWithRemainder(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	var projects []Project
	for i := 0; i < 5; i++ {
		projects = append(projects, Project{
			Name:       "proj",
			Path:       "/p",
			LastCommit: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	out := Render("", projects, 14, 2, now)
	if !strings.Contains(out, "+3 more") {
		t.Errorf("expected remainder line '+3 more':\n%s", out)
	}
}

func TestRender_NoActiveProjectsNote(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	out := Render("profile text", nil, 14, 10, now)
	if !strings.Contains(out, "none in this window") {
		t.Errorf("expected 'no active projects' note:\n%s", out)
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		delta time.Duration
		want  string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{3 * 24 * time.Hour, "3d ago"},
	}
	for _, c := range cases {
		got := relativeTime(now.Add(-c.delta), now)
		if got != c.want {
			t.Errorf("relativeTime(-%v) = %q, want %q", c.delta, got, c.want)
		}
	}

	if got := relativeTime(time.Time{}, now); got != "unknown" {
		t.Errorf("relativeTime(zero) = %q, want unknown", got)
	}
}
