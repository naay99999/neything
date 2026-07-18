package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterClaudeDesktopPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "claude_desktop_config.json")
	orig := `{"preferences": {"theme": "dark"}, "mcpServers": {"other": {"command": "x"}}}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := registerClaudeDesktop(cfg, "/usr/local/bin/ney"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(cfg)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if m["preferences"] == nil {
		t.Fatal("unrelated top-level key was dropped")
	}
	servers := m["mcpServers"].(map[string]any)
	if servers["other"] == nil {
		t.Fatal("unrelated server entry was dropped")
	}
	ney := servers["ney"].(map[string]any)
	if ney["command"] != "/usr/local/bin/ney" {
		t.Fatalf("wrong command: %v", ney)
	}

	// backup written
	if _, err := os.Stat(cfg + ".bak"); err != nil {
		t.Fatal("expected .bak backup of the original config")
	}
	bak, _ := os.ReadFile(cfg + ".bak")
	if string(bak) != orig {
		t.Fatal("backup must contain the pre-modification content")
	}
}

func TestRegisterClaudeDesktopRefusesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "claude_desktop_config.json")
	broken := `{"oops": ` // truncated
	if err := os.WriteFile(cfg, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := registerClaudeDesktop(cfg, "/bin/ney"); err == nil {
		t.Fatal("expected error for unparseable config")
	}
	data, _ := os.ReadFile(cfg)
	if string(data) != broken {
		t.Fatal("unparseable config must be left byte-for-byte untouched")
	}
}

func TestRegisterClaudeDesktopCreatesFreshConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "sub", "claude_desktop_config.json")
	if err := registerClaudeDesktop(cfg, "/bin/ney"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if !strings.Contains(string(data), `"ney"`) {
		t.Fatalf("fresh config missing ney entry: %s", data)
	}
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Fatal("no .bak should be written when there was no original file")
	}
}

func TestRegisterClaudeDesktopRerunIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.json")
	if err := registerClaudeDesktop(cfg, "/bin/ney"); err != nil {
		t.Fatal(err)
	}
	if err := registerClaudeDesktop(cfg, "/new/path/ney"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	servers := m["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Fatalf("rerun must not duplicate entries: %v", servers)
	}
	if servers["ney"].(map[string]any)["command"] != "/new/path/ney" {
		t.Fatal("rerun must update the command path")
	}
}

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
