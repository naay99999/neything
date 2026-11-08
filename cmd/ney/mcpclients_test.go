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

func TestRegisterCodexNoDuplicateTable(t *testing.T) {
	for _, header := range []string{`[mcp_servers.ney] # note`, `[mcp_servers."ney"]`, `  [ mcp_servers.ney ]`} {
		cfg := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(cfg, []byte("model = \"x\"\n\n"+header+"\ncommand = \"/old\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := registerCodex(cfg, "/new/ney"); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(cfg)
		if n := strings.Count(string(data), "mcp_servers"); n != 1 {
			t.Errorf("%q: %d ney tables:\n%s", header, n, data)
		}
		if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o644 {
			t.Errorf("mode changed to %v", fi.Mode().Perm())
		}
	}
}

func TestPreviewsDiscloseRewrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, c := range detectClients("/bin/ney") {
		n := 0
		for _, ch := range c.Changes {
			if strings.Contains(ch.Preview, "re-written with keys sorted") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: rewrite note count = %d", c.Name, n)
		}
	}
}

func TestRegisterHookRefusesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(filepath.Join(dir, "missing.json"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := registerSessionStartHook(link, "/bin/ney", "claude-code"); err == nil {
		t.Fatal("want error for dangling symlink")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink was replaced: %v %v", fi, err)
	}
}
