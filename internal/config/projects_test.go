package config

import (
	"strings"
	"testing"
)

func TestProjectsDefaults(t *testing.T) {
	cfg := writeTestConfig(t, "")
	p := cfg.Projects
	if !p.Inject || p.StaleCommits != 30 || p.StaleDays != 60 || p.NamesInBrief != 20 {
		t.Fatalf("defaults = %+v, want inject=true stale_commits=30 stale_days=60 names_in_brief=20", p)
	}
}

func TestProjectsOverrides(t *testing.T) {
	cfg := writeTestConfig(t, "projects:\n  inject: false\n  stale_commits: 5\n  stale_days: 7\n  names_in_brief: 3\n")
	p := cfg.Projects
	if p.Inject || p.StaleCommits != 5 || p.StaleDays != 7 || p.NamesInBrief != 3 {
		t.Fatalf("overrides = %+v", p)
	}
}

func TestValidateProjects(t *testing.T) {
	for _, key := range []string{"stale_commits", "stale_days", "names_in_brief"} {
		cfg := baseValidCfg()
		switch key {
		case "stale_commits":
			cfg.Projects.StaleCommits = -1
		case "stale_days":
			cfg.Projects.StaleDays = -1
		case "names_in_brief":
			cfg.Projects.NamesInBrief = -1
		}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "projects."+key) {
			t.Errorf("%s = -1: err = %v, want one naming projects.%s", key, err, key)
		}
	}
}
