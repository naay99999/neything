package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterHookToleratesNull(t *testing.T) {
	for name, body := range map[string]string{
		"hooks null":        `{"model":"x","hooks":null}`,
		"SessionStart null": `{"model":"x","hooks":{"Stop":[],"SessionStart":null}}`,
		"file null":         `null`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			changed, err := registerSessionStartHook(path, "/bin/ney", "claude-code")
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			m := readJSON(t, path)
			if body != `null` && m["model"] != "x" {
				t.Error("other keys lost")
			}
			if n := len(neyCommands(t, m)); n != 1 {
				t.Errorf("want 1 ney entry, got %d", n)
			}
		})
	}
}

func TestRegisterHookKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "settings.json")
	if err := os.WriteFile(target, []byte(`{"model":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := registerSessionStartHook(link, "/bin/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was replaced: %v %v", fi, err)
	}
	if n := len(neyCommands(t, readJSON(t, target))); n != 1 {
		t.Errorf("target missing hook")
	}
	if _, err := os.Stat(target + ".bak"); err != nil {
		t.Errorf("backup not next to target: %v", err)
	}
}

func TestRegisterHookNoHTMLEscaping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	orig := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"cd x && make > out <in"}]}]}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registerSessionStartHook(path, "/bin/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"cd x && make > out <in"`) {
		t.Errorf("command rewritten:\n%s", data)
	}
}
