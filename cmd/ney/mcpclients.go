package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// clientReg describes one AI client the wizard can register ney with. Every
// Register implementation backs the target file up as <file>.bak before
// modifying it and refuses to overwrite a file it cannot parse.
type clientReg struct {
	Name     string
	Detected bool
	Register func() error
	// Manual is the copy-paste instruction printed when the client is not
	// detected (or the user declines).
	Manual string
}

// detectClients probes for supported AI clients and returns one entry per
// client, detected or not. neyBin must be the absolute path to the running
// ney binary.
func detectClients(neyBin string) []clientReg {
	home, _ := os.UserHomeDir()

	desktopCfg := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	desktopDir := filepath.Dir(desktopCfg)
	_, desktopErr := os.Stat(desktopDir)

	_, claudeErr := exec.LookPath("claude")

	codexDir := filepath.Join(home, ".codex")
	codexCfg := filepath.Join(codexDir, "config.toml")
	_, codexErr := os.Stat(codexDir)

	return []clientReg{
		{
			Name:     "Claude Desktop",
			Detected: desktopErr == nil,
			Register: func() error { return registerClaudeDesktop(desktopCfg, neyBin) },
			Manual: fmt.Sprintf("Add to %s:\n"+
				`  "mcpServers": {"ney": {"command": %q, "args": ["mcp"]}}`, desktopCfg, neyBin),
		},
		{
			Name:     "Claude Code",
			Detected: claudeErr == nil,
			Register: func() error { return registerClaudeCode(neyBin) },
			Manual:   fmt.Sprintf("Run: claude mcp add --scope user ney -- %s mcp", neyBin),
		},
		{
			Name:     "Codex",
			Detected: codexErr == nil,
			Register: func() error { return registerCodex(codexCfg, neyBin) },
			Manual: fmt.Sprintf("Add to %s:\n"+
				"  [mcp_servers.ney]\n  command = %q\n  args = [\"mcp\"]", codexCfg, neyBin),
		},
	}
}

// backupFile copies path to path.bak when path exists. Called before every
// config modification so a bad write is always recoverable.
func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path+".bak", data, 0o600)
}

// registerClaudeDesktop merges {"mcpServers": {"ney": {...}}} into the
// Claude Desktop JSON config, preserving every other key. A file that exists
// but does not parse is left untouched (error returned) — never clobber a
// config we cannot read.
func registerClaudeDesktop(configPath, neyBin string) error {
	cfg := map[string]any{}
	data, err := os.ReadFile(configPath)
	switch {
	case os.IsNotExist(err):
		// fresh config
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("existing config is not valid JSON (%s): %w — not touching it", configPath, err)
		}
	}

	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["ney"] = map[string]any{"command": neyBin, "args": []any{"mcp"}}
	cfg["mcpServers"] = servers

	if err := backupFile(configPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o600)
}

// registerClaudeCode registers via the claude CLI (user scope). An existing
// ney entry is removed first — `claude mcp add` errors on duplicates, and
// re-registering must be idempotent.
func registerClaudeCode(neyBin string) error {
	rm := exec.Command("claude", "mcp", "remove", "--scope", "user", "ney")
	_ = rm.Run() // best-effort: fails when ney wasn't registered — fine
	add := exec.Command("claude", "mcp", "add", "--scope", "user", "ney", "--", neyBin, "mcp")
	out, err := add.CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// registerCodex writes the [mcp_servers.ney] section into Codex's TOML
// config: replaced in place when present, appended otherwise. The file is
// treated as text — replacing one bracketed section is well-defined without
// a TOML parser, and everything outside the section is preserved verbatim.
func registerCodex(configPath, neyBin string) error {
	section := fmt.Sprintf("[mcp_servers.ney]\ncommand = %q\nargs = [\"mcp\"]\n", neyBin)

	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var out string
	if err == nil {
		lines := strings.Split(string(data), "\n")
		start := -1
		for i, ln := range lines {
			if strings.TrimSpace(ln) == "[mcp_servers.ney]" {
				start = i
				break
			}
		}
		if start >= 0 {
			end := len(lines)
			for i := start + 1; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
					end = i
					break
				}
			}
			head := strings.Join(lines[:start], "\n")
			tail := strings.Join(lines[end:], "\n")
			out = strings.TrimRight(head, "\n")
			if out != "" {
				out += "\n"
			}
			out += section
			if strings.TrimSpace(tail) != "" {
				out += tail
			}
		} else {
			out = string(data)
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += "\n" + section
		}
	} else {
		out = section
	}

	if err := backupFile(configPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(configPath, []byte(out), 0o600)
}
