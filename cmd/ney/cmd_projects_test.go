package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/projects"
)

func execProjects(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags(rootCmd)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"projects"}, args...))
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	return out.String(), err
}

func TestProjectsCommandList(t *testing.T) {
	setHome(t, t.TempDir())
	repo := filepath.Join(t.TempDir(), "alpha")
	initGitFixtureRepo(t, repo, "c")
	if _, err := updateProjectCard(context.Background(), repo, "test", summaryPatch("Alpha does things."), time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := execProjects(t)
	if err != nil || !strings.Contains(out, "- alpha · ") || !strings.Contains(out, "Alpha does things.") {
		t.Fatalf("err=%v out:\n%s", err, out)
	}
	out, err = execProjects(t, "show", "alpha")
	if err != nil || !strings.Contains(out, "Alpha does things.") || !strings.Contains(out, "Live: branch") {
		t.Fatalf("show: err=%v out:\n%s", err, out)
	}
}

func TestProjectsCommandShowUnknown(t *testing.T) {
	setHome(t, t.TempDir())
	repo := filepath.Join(t.TempDir(), "alpha")
	initGitFixtureRepo(t, repo, "c")
	if _, err := updateProjectCard(context.Background(), repo, "test", summaryPatch("x"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := execProjects(t, "show", "alph"); err == nil || !strings.Contains(err.Error(), "closest: alpha") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoctorProjectChecks(t *testing.T) {
	setHome(t, t.TempDir())
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "alpha")
	initGitFixtureRepo(t, repo, "c")
	ctx := context.Background()
	res := projectChecks(ctx, cfg, repo, time.Now())
	if len(res) != 2 || res[0].Message != "Project cards: 0" || res[1].OK || res[1].Message != "Project card: missing" ||
		res[1].Hint != "Open an agent here; it will be asked to write one" {
		t.Fatalf("missing: %+v", res)
	}
	if _, err := updateProjectCard(ctx, repo, "test", summaryPatch("x"), time.Now()); err != nil {
		t.Fatal(err)
	}
	res = projectChecks(ctx, cfg, repo, time.Now())
	if res[0].Message != "Project cards: 1" || !res[1].OK || res[1].Message != "Project card: fresh" {
		t.Fatalf("fresh: %+v", res)
	}
	if res := projectChecks(ctx, cfg, t.TempDir(), time.Now()); len(res) != 1 {
		t.Errorf("outside a repo: %+v", res)
	}
}

func summaryPatch(s string) projects.Patch { return projects.Patch{Summary: &s} }
