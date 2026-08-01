package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
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
	_, err := config.Load()
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

	results = append(results, checkResult{
		Check:   "loaders",
		OK:      true,
		Message: "Supported formats: .md, .markdown, .txt (+ Obsidian/Notion .md)",
	})

	// 2. SQLite writable
	db, err := store.Open(config.DBPath())
	if err != nil {
		results = append(results, checkResult{
			Check:   "sqlite_writable",
			OK:      false,
			Message: fmt.Sprintf("SQLite not writable: %v", err),
			Hint:    "Check permissions on " + config.NeyDir(),
		})
	} else {
		db.Close()
		results = append(results, checkResult{
			Check:   "sqlite_writable",
			OK:      true,
			Message: fmt.Sprintf("SQLite DB writable (%s)", config.DBPath()),
		})

		// 3. Index has data
		db2, _ := store.Open(config.DBPath())
		if db2 != nil {
			defer db2.Close()
			stats, _ := db2.Stats()
			if stats != nil && stats.ChunkCount > 0 {
				results = append(results, checkResult{
					Check:   "index_has_data",
					OK:      true,
					Message: fmt.Sprintf("Index contains %d chunks across %d files", stats.ChunkCount, stats.DocumentCount),
				})
			} else {
				results = append(results, checkResult{
					Check:   "index_has_data",
					OK:      false,
					Message: "Index is empty",
					Hint:    "Run: ney index <path>",
				})
			}
		}
	}

	// 4. MCP readiness — always informational (pass-only): tells the user the
	// exact command to wire ney into an MCP client, with the actual resolved
	// binary path so it works regardless of how `ney` was installed/aliased.
	results = append(results, checkResult{
		Check:   "mcp",
		OK:      true,
		Message: "MCP server available — point an AI client at ney for zero-setup search",
		// Args-less on purpose: the workspaces table is the single source of
		// truth for what gets served (see README "MCP — connect your AI").
		Hint: fmt.Sprintf("claude mcp add --scope user ney -- %s mcp", resolveNeyBinary()),
	})

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
