package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/config"
)

// codexEnv isolates HOME and CODEX_HOME and hides any real codex binary.
func codexEnv(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", t.TempDir())
	return home
}

func codexClient(t *testing.T) clientReg {
	t.Helper()
	for _, c := range detectClients("/bin/ney") {
		if c.Name == "Codex" {
			return c
		}
	}
	t.Fatal("no Codex client")
	return clientReg{}
}

func TestSourcesForCodex(t *testing.T) {
	home := codexEnv(t)
	cfg := &config.Config{Sources: []string{"codex"}}
	if got := sourcesFor(cfg); len(got) != 0 {
		t.Fatalf("no ~/.codex: sources = %d, want 0", len(got))
	}
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := sourcesFor(cfg)
	if len(got) != 1 || got[0].Name() != "codex" {
		t.Fatalf("sources = %v", got)
	}
	cfg.Sources = []string{"claude-code", "codex"}
	if got := sourcesFor(cfg); len(got) != 2 {
		t.Errorf("both enabled: sources = %d", len(got))
	}
}

func TestDetectCodexUsesCodexHome(t *testing.T) {
	home := codexEnv(t)
	dir := filepath.Join(home, "elsewhere")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)
	c := codexClient(t)
	if !c.Detected || len(c.Changes) != 2 {
		t.Fatalf("Detected=%v changes=%d", c.Detected, len(c.Changes))
	}
	if !strings.Contains(c.Changes[0].Title, filepath.Join(dir, "config.toml")) ||
		!strings.Contains(c.Changes[1].Title, filepath.Join(dir, "hooks.json")) {
		t.Errorf("titles = %q / %q", c.Changes[0].Title, c.Changes[1].Title)
	}
	for _, ch := range c.Changes {
		if err := ch.Apply(); err != nil {
			t.Fatal(err)
		}
	}
	if ok, cmd := hookInstalled(filepath.Join(dir, "hooks.json"), "codex"); !ok || !strings.Contains(cmd, "--agent codex") {
		t.Errorf("hook not installed: %v %q", ok, cmd)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.toml")); !strings.Contains(string(b), "[mcp_servers.ney]") {
		t.Errorf("config.toml = %q", b)
	}
	if c.Changes[1].After == "" || !strings.Contains(c.Changes[1].After, "/hooks") {
		t.Errorf("hook change should print the /hooks trust note, got %q", c.Changes[1].After)
	}
}

func TestDetectCodexWithoutHome(t *testing.T) {
	home := codexEnv(t)
	c := codexClient(t)
	if c.Detected {
		t.Error("Detected without ~/.codex or codex on PATH")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Error("detectClients must not create ~/.codex")
	}
	if !strings.Contains(c.Manual, filepath.Join(home, ".codex")) {
		t.Errorf("Manual should point at ~/.codex: %q", c.Manual)
	}
	// Applying creates the directory.
	if err := c.Changes[1].Apply(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := hookInstalled(filepath.Join(home, ".codex", "hooks.json"), "codex"); !ok {
		t.Error("hook not installed under ~/.codex")
	}
}

func TestDetectCodexOnPath(t *testing.T) {
	codexEnv(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if !codexClient(t).Detected {
		t.Error("codex on PATH should count as detected")
	}
}

func TestCodexChecks(t *testing.T) {
	home := codexEnv(t)
	cfg := &config.Config{Sources: []string{"codex"}}
	byCheck := func(rs []checkResult) map[string]checkResult {
		m := map[string]checkResult{}
		for _, r := range rs {
			m[r.Check] = r
		}
		return m
	}
	m := byCheck(codexChecks(cfg))
	if r := m["codex_hook"]; r.OK || r.Message != "Codex hook: not installed" {
		t.Errorf("hook = %+v", r)
	}
	if r := m["codex_sessions"]; r.Message != "Codex sessions: 0" {
		t.Errorf("sessions = %+v", r)
	}

	dir := filepath.Join(home, ".codex")
	day := filepath.Join(dir, "sessions", "2026", "11", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"rollout-a.jsonl", "rollout-b.jsonl", "other.jsonl"} {
		if err := os.WriteFile(filepath.Join(day, n), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registerSessionStartHook(filepath.Join(dir, "hooks.json"), "/bin/ney", "codex"); err != nil {
		t.Fatal(err)
	}
	m = byCheck(codexChecks(cfg))
	if r := m["codex_hook"]; !strings.HasPrefix(r.Message, "Codex hook: installed") {
		t.Errorf("hook = %+v", r)
	}
	if r := m["codex_sessions"]; r.Message != "Codex sessions: 2" {
		t.Errorf("sessions = %+v", r)
	}

	if rs := codexChecks(&config.Config{Sources: []string{"claude-code"}}); len(rs) != 0 {
		t.Errorf("codex not in sources: got %+v", rs)
	}
}
