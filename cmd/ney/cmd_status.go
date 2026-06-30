package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show index statistics",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	vs, err := vectorstore.NewBruteForceStore(config.VectorsPath())
	if err != nil {
		return err
	}

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	lastIndexed, _ := db.GetMeta("last_indexed_at")
	vecCount := vs.Count()

	var dbSize int64
	if fi, err := os.Stat(config.DBPath()); err == nil {
		dbSize = fi.Size()
	}

	type statusOut struct {
		Workspaces  int    `json:"workspaces"`
		Documents   int    `json:"documents"`
		Chunks      int    `json:"chunks"`
		Vectors     int    `json:"vectors"`
		DBSizeBytes int64  `json:"db_size_bytes"`
		LastIndexed string `json:"last_indexed"`
	}
	out := statusOut{
		Workspaces:  stats.WorkspaceCount,
		Documents:   stats.DocumentCount,
		Chunks:      stats.ChunkCount,
		Vectors:     vecCount,
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
			{"Vectors", fmt.Sprintf("%d", out.Vectors)},
			{"DB size", fmt.Sprintf("%.1f KB", float64(out.DBSizeBytes)/1024)},
			{"Last indexed", lastIndexed},
		},
	)
	return nil
}
