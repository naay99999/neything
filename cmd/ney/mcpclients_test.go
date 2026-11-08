package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterCodexAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	orig := "model = \"o4\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := registerCodex(cfg, "/bin/ney"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	s := string(data)
	if !strings.Contains(s, "model = \"o4\"") || !strings.Contains(s, "[mcp_servers.other]") {
		t.Fatalf("existing content lost: %s", s)
	}
	if !strings.Contains(s, "[mcp_servers.ney]") || !strings.Contains(s, `command = "/bin/ney"`) {
		t.Fatalf("ney section missing: %s", s)
	}
}

func TestRegisterCodexReplacesExistingSection(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	orig := "[mcp_servers.ney]\ncommand = \"/old/ney\"\nargs = [\"mcp\", \"--root\", \"/x\"]\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := registerCodex(cfg, "/new/ney"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	s := string(data)
	if strings.Contains(s, "/old/ney") || strings.Contains(s, "--root") {
		t.Fatalf("old section not fully replaced: %s", s)
	}
	if !strings.Contains(s, `command = "/new/ney"`) {
		t.Fatalf("new command missing: %s", s)
	}
	if !strings.Contains(s, "[mcp_servers.other]") {
		t.Fatalf("sibling section lost: %s", s)
	}
	if strings.Count(s, "[mcp_servers.ney]") != 1 {
		t.Fatalf("expected exactly one ney section: %s", s)
	}
}

func TestRegisterCodexFreshFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	if err := registerCodex(cfg, "/bin/ney"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(data), "[mcp_servers.ney]") {
		t.Fatalf("fresh file wrong: %s", data)
	}
}
