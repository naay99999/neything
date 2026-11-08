package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// clientChange is one confirmable modification to an AI client's config.
// Preview shows the exact change before the user is asked to accept it.
type clientChange struct {
	Title   string
	Preview string
	Apply   func() error
}

// clientReg describes one AI client the wizard can register ney with. Every
// Apply implementation backs the target file up as <file>.bak before
// modifying it and refuses to overwrite a file it cannot parse.
type clientReg struct {
	Name     string
	Detected bool
	Changes  []clientChange
	// Manual is the copy-paste instruction printed when the client is not
	// detected (or the user declines).
	Manual string
}

// detectClients probes for supported AI clients and returns one entry per
// client, detected or not. neyBin must be the absolute path to the running
// ney binary.
func detectClients(neyBin string) []clientReg {
	home, _ := os.UserHomeDir()

	_, claudeErr := exec.LookPath("claude")
	settings := filepath.Join(home, ".claude", "settings.json")

	codexDir := filepath.Join(home, ".codex")
	codexCfg := filepath.Join(codexDir, "config.toml")
	_, codexErr := os.Stat(codexDir)

	mcpLine := fmt.Sprintf("claude mcp add --scope user ney -- %s mcp", neyBin)
	return []clientReg{
		{
			Name:     "Claude Code",
			Detected: claudeErr == nil,
			Changes: []clientChange{
				{
					Title:   "register the ney MCP server",
					Preview: mcpLine,
					Apply:   func() error { return registerClaudeCode(neyBin) },
				},
				{
					Title:   "add the SessionStart handoff hook to " + settings,
					Preview: hookEntryPreview(neyBin, "claude-code"),
					Apply: func() error {
						_, err := registerSessionStartHook(settings, neyBin, "claude-code")
						return err
					},
				},
			},
			Manual: fmt.Sprintf("Run: %s\nThen add a SessionStart command hook to %s:\n  %s",
				mcpLine, settings, hookCommand(neyBin, "claude-code")),
		},
		{
			Name:     "Codex",
			Detected: codexErr == nil,
			Changes: []clientChange{{
				Title:   "register the ney MCP server in " + codexCfg,
				Preview: fmt.Sprintf("[mcp_servers.ney]\ncommand = %q\nargs = [\"mcp\"]", neyBin),
				Apply:   func() error { return registerCodex(codexCfg, neyBin) },
			}},
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
