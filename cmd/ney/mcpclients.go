package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/naay99999/neything/internal/session/codex"
)

// clientChange is one confirmable modification to an AI client's config.
// Preview shows the exact change before the user is asked to accept it.
type clientChange struct {
	Title   string
	Preview string
	Apply   func() error
	// After is printed once Apply succeeds (optional follow-up the user must do).
	After string
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

	// Codex counts as detected when its home exists or the binary is on PATH.
	// Without a home dir we target ~/.codex; the directory is created only when
	// a change is applied.
	codexDir, codexHomeOK := codex.Home()
	if !codexHomeOK {
		codexDir = filepath.Join(home, ".codex")
	}
	_, codexBinErr := exec.LookPath("codex")
	codexCfg := filepath.Join(codexDir, "config.toml")
	codexHooks := filepath.Join(codexDir, "hooks.json")

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
					Preview: hookEntryPreview(neyBin, "claude-code") + "\n" + hookRewriteNote(settings),
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
			Detected: codexHomeOK || codexBinErr == nil,
			Changes: []clientChange{
				{
					Title:   "register the ney MCP server in " + codexCfg,
					Preview: fmt.Sprintf("[mcp_servers.ney]\ncommand = %q\nargs = [\"mcp\"]", neyBin),
					Apply:   func() error { return registerCodex(codexCfg, neyBin) },
				},
				{
					Title:   "add the SessionStart handoff hook to " + codexHooks,
					Preview: hookEntryPreview(neyBin, "codex") + "\n" + hookRewriteNote(codexHooks),
					Apply: func() error {
						_, err := registerSessionStartHook(codexHooks, neyBin, "codex")
						return err
					},
					After: "Codex runs new hooks only after you trust them: run /hooks inside Codex once.",
				},
			},
			Manual: fmt.Sprintf("Add to %s:\n"+
				"  [mcp_servers.ney]\n  command = %q\n  args = [\"mcp\"]\n"+
				"Then add a SessionStart command hook to %s:\n  %s",
				codexCfg, neyBin, codexHooks, hookCommand(neyBin, "codex")),
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

	if resolved, rerr := filepath.EvalSymlinks(configPath); rerr == nil {
		configPath = resolved
	} else if fi, lerr := os.Lstat(configPath); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a dangling symlink — not replacing it", configPath)
	}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var out string
	if err == nil {
		lines := strings.Split(string(data), "\n")
		start := -1
		for i, ln := range lines {
			if codexNeyTable.MatchString(ln) {
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
	return writeFileAtomic(configPath, []byte(out))
}

// codexNeyTable matches the ney MCP table header, tolerating whitespace,
// a quoted key and a trailing comment.
var codexNeyTable = regexp.MustCompile(`(?m)^\s*\[\s*mcp_servers\.(?:ney|"ney")\s*\]\s*(?:#.*)?$`)
