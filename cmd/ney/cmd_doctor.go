package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system readiness",
	RunE:  runDoctor,
}

type checkResult struct {
	Check   string `json:"check"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func runDoctor(cmd *cobra.Command, args []string) error {
	var results []checkResult

	// 1. Config valid
	cfg, err := config.Load()
	if err != nil {
		results = append(results, checkResult{
			Check:   "config_valid",
			OK:      false,
			Message: fmt.Sprintf("Config error: %v", err),
			Hint:    "Edit ~/.ney/config.yaml",
		})
		printDoctorResults(results)
		return nil
	}
	results = append(results, checkResult{
		Check:   "config_valid",
		OK:      true,
		Message: fmt.Sprintf("Config file valid (%s)", config.ConfigPath()),
	})

	// 2. MCP readiness — always informational (pass-only): tells the user the
	// exact command to wire ney into an MCP client, with the actual resolved
	// binary path so it works regardless of how `ney` was installed/aliased.
	results = append(results, checkResult{
		Check:   "mcp",
		OK:      true,
		Message: "MCP server available — point an AI client at ney for session handoff and project memory",
		// Args-less on purpose: `ney mcp` needs no configuration.
		Hint: fmt.Sprintf("claude mcp add --scope user ney -- %s mcp", resolveNeyBinary()),
	})

	results = append(results, claudeCodeChecks(cfg)...)
	results = append(results, codexChecks(cfg)...)
	if cwd, err := filepath.Abs("."); err == nil {
		ctx := context.Background()
		if cmd != nil && cmd.Context() != nil {
			ctx = cmd.Context()
		}
		results = append(results, projectChecks(ctx, cfg, cwd, time.Now())...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		results = append(results, skillChecks(home)...)
	}

	printDoctorResults(results)
	return nil
}

func printDoctorResults(results []checkResult) {
	if flagJSON {
		PrintJSON(results)
		return
	}
	allOK := true
	for _, r := range results {
		mark := CheckMark(r.OK)
		fmt.Printf("%s %s\n", mark, r.Message)
		if r.Hint != "" {
			fmt.Printf("    → %s\n", r.Hint)
		}
		if !r.OK {
			allOK = false
		}
	}
	if allOK {
		fmt.Println("\nAll checks passed. Ney is ready!")
	}
}
