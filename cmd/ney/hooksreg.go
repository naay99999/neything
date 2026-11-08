package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const hookTimeoutSeconds = 10

// hookCommand is the exact command string ney writes for agent, and the shape
// used to recognise its own entry later. neyBin is single-quoted only when it
// contains characters outside [A-Za-z0-9_./-].
func hookCommand(neyBin, agent string) string {
	return shellQuote(neyBin) + hookSuffix(agent)
}

func hookSuffix(agent string) string { return " hook session-start --agent " + agent }

func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '/' || r == '-') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookBinary extracts the executable path from a command written by hookCommand.
func hookBinary(cmd, agent string) string {
	bin := strings.TrimSuffix(cmd, hookSuffix(agent))
	if len(bin) >= 2 && bin[0] == '\'' && bin[len(bin)-1] == '\'' {
		bin = strings.ReplaceAll(bin[1:len(bin)-1], `'\''`, "'")
	}
	return bin
}

// hookEntryPreview is the JSON snippet shown to the user before it is written.
func hookEntryPreview(neyBin, agent string) string {
	out, _ := json.MarshalIndent(map[string]any{
		"type": "command", "command": hookCommand(neyBin, agent), "timeout": hookTimeoutSeconds,
	}, "", "  ")
	return string(out)
}

// hookRewriteNote discloses that registering re-serialises the whole file.
func hookRewriteNote(path string) string {
	return "Note: " + path + " will be re-written with keys sorted and 2-space indentation (a .bak copy is kept)."
}

func isNeyHook(h map[string]json.RawMessage, agent string) (string, bool) {
	var cmd string
	if raw, ok := h["command"]; !ok || json.Unmarshal(raw, &cmd) != nil {
		return "", false
	}
	return cmd, strings.HasSuffix(cmd, hookSuffix(agent))
}

// parseHooksFile decodes the SessionStart groups of a hooks file. Every
// level is kept as raw JSON maps so unknown fields round-trip untouched.
func parseHooksFile(data []byte) (top, hooks map[string]json.RawMessage, groups []map[string]json.RawMessage, err error) {
	top = map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err = json.Unmarshal(data, &top); err != nil {
			return
		}
		if top == nil { // file content was JSON null
			top = map[string]json.RawMessage{}
		}
	}
	hooks = map[string]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err = json.Unmarshal(raw, &hooks); err != nil {
			return
		}
		if hooks == nil {
			hooks = map[string]json.RawMessage{}
		}
	}
	if raw, ok := hooks["SessionStart"]; ok {
		if err = json.Unmarshal(raw, &groups); err != nil {
			return
		}
	}
	return
}

// groupHooks decodes a group's "hooks" array.
func groupHooks(g map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	var hs []map[string]json.RawMessage
	if raw, ok := g["hooks"]; ok {
		if err := json.Unmarshal(raw, &hs); err != nil {
			return nil, err
		}
	}
	return hs, nil
}

// encodeJSON marshals v without HTML escaping (so users' `&&`, `<`, `>` in
// commands are never rewritten). indent adds 2-space indentation.
func encodeJSON(v any, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func mustRaw(v any) json.RawMessage {
	b, _ := encodeJSON(v, false)
	return b
}

// registerSessionStartHook merges a SessionStart command hook into a
// Claude-style hooks file ({"hooks":{"SessionStart":[{"hooks":[...]}]}}). The
// file is backed up to <path>.bak before a change, an unparseable file is
// refused untouched, all other keys, groups and hook fields are preserved, and
// an existing ney entry for agent is updated in place rather than duplicated.
func registerSessionStartHook(path, neyBin, agent string) (changed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	top, hooks, groups, err := parseHooksFile(data)
	if err != nil {
		return false, fmt.Errorf("existing hooks file is not valid JSON in the expected shape (%s): %w — not touching it", path, err)
	}

	want := hookCommand(neyBin, agent)
	found := false
	var outGroups []map[string]json.RawMessage
	for _, g := range groups {
		hs, err := groupHooks(g)
		if err != nil {
			return false, fmt.Errorf("unexpected hooks shape in %s: %w — not touching it", path, err)
		}
		var kept []map[string]json.RawMessage
		groupChanged := false
		for _, h := range hs {
			cmd, ney := isNeyHook(h, agent)
			if !ney {
				kept = append(kept, h)
				continue
			}
			if found { // duplicate ney entry: drop
				groupChanged = true
				continue
			}
			found = true
			var timeout int
			_ = json.Unmarshal(h["timeout"], &timeout)
			if cmd != want || timeout != hookTimeoutSeconds {
				h["command"] = mustRaw(want)
				h["timeout"] = mustRaw(hookTimeoutSeconds)
				groupChanged = true
			}
			kept = append(kept, h)
		}
		if groupChanged {
			changed = true
			if len(kept) == 0 {
				continue // group held only a duplicate ney entry
			}
			g["hooks"] = mustRaw(kept)
		}
		outGroups = append(outGroups, g)
	}
	if !found {
		changed = true
		outGroups = append(outGroups, map[string]json.RawMessage{
			"hooks": mustRaw([]any{map[string]any{
				"type": "command", "command": want, "timeout": hookTimeoutSeconds,
			}}),
		})
	}
	if !changed {
		return false, nil
	}

	hooks["SessionStart"] = mustRaw(outGroups)
	top["hooks"] = mustRaw(hooks)
	out, err := encodeJSON(top, true)
	if err != nil {
		return false, err
	}
	// Write through a symlink: update its target, not the link itself.
	resolved, rerr := filepath.EvalSymlinks(path)
	if rerr == nil {
		path = resolved
	} else if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a dangling symlink — not replacing it", path)
	}
	if err := backupFile(path); err != nil {
		return false, err
	}
	return true, writeFileAtomic(path, append(out, '\n'))
}

// writeFileAtomic writes via temp file + rename, keeping an existing file's
// mode (0o600 for a new file).
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// hookInstalled reports whether path holds a ney SessionStart hook for agent,
// and its command.
func hookInstalled(path, agent string) (installed bool, cmd string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	_, _, groups, err := parseHooksFile(data)
	if err != nil {
		return false, ""
	}
	for _, g := range groups {
		hs, err := groupHooks(g)
		if err != nil {
			continue
		}
		for _, h := range hs {
			if c, ok := isNeyHook(h, agent); ok {
				return true, c
			}
		}
	}
	return false, ""
}

// claudeSettingsPath is Claude Code's user settings file.
func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}
