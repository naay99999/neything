package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show index statistics",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	if _, err := loadConfig(); err != nil {
		return err
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	lastIndexed, _ := db.GetMeta("last_indexed_at")

	var dbSize int64
	if fi, err := os.Stat(config.DBPath()); err == nil {
		dbSize = fi.Size()
	}

	type statusOut struct {
		Workspaces  int    `json:"workspaces"`
		Documents   int    `json:"documents"`
		Chunks      int    `json:"chunks"`
		DBSizeBytes int64  `json:"db_size_bytes"`
		LastIndexed string `json:"last_indexed"`
	}
	out := statusOut{
		Workspaces:  stats.WorkspaceCount,
		Documents:   stats.DocumentCount,
		Chunks:      stats.ChunkCount,
		DBSizeBytes: dbSize,
		LastIndexed: lastIndexed,
	}

	if flagJSON {
		PrintJSON(out)
		return nil
	}

	PrintTable(
		[]string{"Metric", "Value"},
		[][]string{
			{"Workspaces", fmt.Sprintf("%d", out.Workspaces)},
			{"Documents", fmt.Sprintf("%d", out.Documents)},
			{"Chunks", fmt.Sprintf("%d", out.Chunks)},
			{"DB size", fmt.Sprintf("%.1f KB", float64(out.DBSizeBytes)/1024)},
			{"Last indexed", lastIndexed},
		},
	)
	return nil
}
