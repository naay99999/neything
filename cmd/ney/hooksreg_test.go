package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, data)
	}
	return m
}

// neyCommands returns every SessionStart hook command containing "hook session-start".
func neyCommands(t *testing.T, m map[string]any) []string {
	t.Helper()
	var out []string
	hooks, _ := m["hooks"].(map[string]any)
	groups, _ := hooks["SessionStart"].([]any)
	for _, g := range groups {
		inner, _ := g.(map[string]any)["hooks"].([]any)
		for _, h := range inner {
			if c, _ := h.(map[string]any)["command"].(string); strings.Contains(c, "hook session-start") {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestHookCommandQuoting(t *testing.T) {
	cases := []struct{ goos, bin, want string }{
		{"darwin", "/usr/local/bin/ney", "/usr/local/bin/ney"},
		{"linux", "/Users/a b/it's/ney", `'/Users/a b/it'\''s/ney'`},
		{"windows", `C:\Users\naay\AppData\Local\Programs\ney\ney.exe`, "C:/Users/naay/AppData/Local/Programs/ney/ney.exe"},
		{"windows", `C:\Users\John Smith\ney\ney.exe`, `"C:/Users/John Smith/ney/ney.exe"`},
	}
	for _, c := range cases {
		if got := shellQuoteFor(c.goos, c.bin); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.goos, c.bin, got, c.want)
		}
		cmd := shellQuoteFor(c.goos, c.bin) + hookSuffix("codex")
		if back := hookBinary(cmd, "codex"); back != strings.Trim(c.want, `"'`) && !strings.Contains(c.want, `'\''`) {
			t.Errorf("hookBinary(%q) = %q", cmd, back)
		}
	}
	if got := hookCommand("/usr/local/bin/ney", "claude-code"); !strings.HasSuffix(got, " hook session-start --agent claude-code") {
		t.Errorf("hookCommand: %q", got)
	}
	if back := hookBinary(`'/Users/a b/it'\''s/ney' hook session-start --agent codex`, "codex"); back != "/Users/a b/it's/ney" {
		t.Errorf("hookBinary single-quoted: %q", back)
	}
}

func TestRegisterHookFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	changed, err := registerSessionStartHook(path, "/bin/ney", "claude-code")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m := readJSON(t, path)
	groups := m["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(groups))
	}
	h := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if h["type"] != "command" || h["command"] != "/bin/ney hook session-start --agent claude-code" || h["timeout"] != float64(10) {
		t.Errorf("bad entry: %v", h)
	}
}

func TestRegisterHookPreservesUserHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	orig := `{"permissions":{"allow":["Bash(ls)"]},"model":"x","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"pre"}]}],"SessionStart":[{"matcher":"startup","extra":1,"hooks":[{"type":"command","command":"echo hi","timeout":5,"custom":true}]}]}}`
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	changed, err := registerSessionStartHook(path, "/bin/ney", "claude-code")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m := readJSON(t, path)
	if m["model"] != "x" || m["permissions"] == nil {
		t.Fatal("unrelated keys lost")
	}
	hooks := m["hooks"].(map[string]any)
	if hooks["PreToolUse"] == nil {
		t.Fatal("other hook event lost")
	}
	groups := hooks["SessionStart"].([]any)
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(groups))
	}
	user := groups[0].(map[string]any)
	if user["matcher"] != "startup" || user["extra"] != float64(1) {
		t.Errorf("user group fields lost: %v", user)
	}
	uh := user["hooks"].([]any)[0].(map[string]any)
	if uh["command"] != "echo hi" || uh["custom"] != true || uh["timeout"] != float64(5) {
		t.Errorf("user hook fields lost: %v", uh)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || string(bak) != orig {
		t.Errorf("backup missing or wrong: %v", err)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Errorf("mode not preserved: %v", fi.Mode().Perm())
	}
}

func TestRegisterHookIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := registerSessionStartHook(path, "/bin/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	changed, err := registerSessionStartHook(path, "/bin/ney", "claude-code")
	if err != nil || changed {
		t.Fatalf("second call changed=%v err=%v", changed, err)
	}
	if n := len(neyCommands(t, readJSON(t, path))); n != 1 {
		t.Fatalf("want 1 ney entry, got %d", n)
	}
}

func TestRegisterHookReplacesStalePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := registerSessionStartHook(path, "/old/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	changed, err := registerSessionStartHook(path, "/new/ney", "claude-code")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	cmds := neyCommands(t, readJSON(t, path))
	if len(cmds) != 1 || cmds[0] != "/new/ney hook session-start --agent claude-code" {
		t.Fatalf("got %v", cmds)
	}
}

func TestRegisterHookOtherAgentUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	if _, err := registerSessionStartHook(path, "/bin/ney", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := registerSessionStartHook(path, "/bin/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	if n := len(neyCommands(t, readJSON(t, path))); n != 2 {
		t.Fatalf("want 2 entries (one per agent), got %d", n)
	}
}

func TestRegisterHookRefusesInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const bad = `{"hooks": oops`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registerSessionStartHook(path, "/bin/ney", "claude-code"); err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != bad {
		t.Errorf("file modified: %s", got)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("backup written for a file we refused")
	}
}

func TestHookInstalled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if ok, _ := hookInstalled(path, "claude-code"); ok {
		t.Fatal("installed on missing file")
	}
	if _, err := registerSessionStartHook(path, "/bin/ney", "claude-code"); err != nil {
		t.Fatal(err)
	}
	ok, cmd := hookInstalled(path, "claude-code")
	if !ok || cmd != "/bin/ney hook session-start --agent claude-code" {
		t.Fatalf("ok=%v cmd=%q", ok, cmd)
	}
	if ok, _ := hookInstalled(path, "codex"); ok {
		t.Fatal("codex reported installed")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestDoctorReportsHook(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("PATH", t.TempDir()) // no `claude` on PATH
	flagJSON = false

	proj := filepath.Join(home, ".claude", "projects", "-tmp-x")
	if err := os.MkdirAll(filepath.Join(proj, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.jsonl", "b.jsonl", "subagents/c.jsonl", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(proj, f), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() { _ = runDoctor(nil, nil) })
	if !strings.Contains(out, "Claude Code hook: not installed") {
		t.Errorf("missing not-installed line:\n%s", out)
	}
	if !strings.Contains(out, "Claude Code sessions: 2") {
		t.Errorf("missing sessions line:\n%s", out)
	}
	if !strings.Contains(out, "Claude Code CLI: not found") {
		t.Errorf("missing CLI line:\n%s", out)
	}

	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := registerSessionStartHook(settings, os.Args[0], "claude-code"); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { _ = runDoctor(nil, nil) })
	if !strings.Contains(out, "Claude Code hook: installed") {
		t.Errorf("missing installed line:\n%s", out)
	}
}
