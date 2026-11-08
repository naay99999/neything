package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
)

// claudeCodeChecks reports whether the Claude Code side of session handoff is
// wired: CLI + MCP registration, the SessionStart hook, transcripts found, and
// whether a brief can be built for the current directory.
func claudeCodeChecks(cfg *config.Config) []checkResult {
	var rs []checkResult

	if _, err := exec.LookPath("claude"); err != nil {
		rs = append(rs, checkResult{
			Check: "claude_cli", OK: true,
			Message: "Claude Code CLI: not found",
			Hint:    "Install Claude Code to use ney with it; skip if you use another client",
		})
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "claude", "mcp", "get", "ney").Run(); err != nil {
			rs = append(rs, checkResult{
				Check: "claude_mcp", OK: false,
				Message: "Claude Code MCP: ney not registered",
				Hint:    fmt.Sprintf("claude mcp add --scope user ney -- %s mcp", resolveNeyBinary()),
			})
		} else {
			rs = append(rs, checkResult{Check: "claude_mcp", OK: true, Message: "Claude Code MCP: ney registered"})
		}
	}

	if installed, cmd := hookInstalled(claudeSettingsPath(), "claude-code"); !installed {
		rs = append(rs, checkResult{
			Check: "claude_hook", OK: false,
			Message: "Claude Code hook: not installed",
			Hint:    "Run `ney init` to add the SessionStart handoff hook",
		})
	} else if bin := hookBinary(cmd, "claude-code"); !binaryExists(bin) {
		rs = append(rs, checkResult{
			Check: "claude_hook", OK: false,
			Message: fmt.Sprintf("Claude Code hook: installed, but binary not found (%s)", bin),
			Hint:    "Run `ney init` to refresh the hook path",
		})
	} else {
		rs = append(rs, checkResult{Check: "claude_hook", OK: true, Message: "Claude Code hook: installed (" + bin + ")"})
	}

	for _, name := range cfg.Sources {
		if name != "claude-code" {
			continue
		}
		rs = append(rs, checkResult{
			Check: "claude_sessions", OK: true,
			Message: fmt.Sprintf("Claude Code sessions: %d", countClaudeSessions()),
		})
	}

	if cwd, err := os.Getwd(); err == nil {
		brief, _, err := buildBrief(context.Background(), cfg, handoffRequest{Cwd: cwd}, false)
		switch {
		case err != nil:
			rs = append(rs, checkResult{Check: "handoff_brief", OK: false, Message: "Handoff brief: " + err.Error()})
		case brief.Primary == nil:
			rs = append(rs, checkResult{Check: "handoff_brief", OK: true, Message: "Handoff brief: no recent sessions"})
		default:
			rs = append(rs, checkResult{Check: "handoff_brief", OK: true, Message: "Handoff brief: ready for " + brief.RepoName})
		}
	}
	return rs
}

func binaryExists(bin string) bool {
	if strings.ContainsRune(bin, filepath.Separator) {
		fi, err := os.Stat(bin)
		return err == nil && !fi.IsDir()
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// countClaudeSessions counts *.jsonl transcripts directly under
// ~/.claude/projects/*/ (subagent transcripts live deeper and are excluded).
func countClaudeSessions() int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "*.jsonl"))
	return len(files)
}
