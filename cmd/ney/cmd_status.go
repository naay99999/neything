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
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	vs, err := config.NewVectorStore(cfg, db, false)
	if err != nil {
		return err
	}

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	lastIndexed, _ := db.GetMeta("last_indexed_at")
	vecBackend, _ := db.GetMeta("vector_store_backend")
	if vecBackend == "" {
		vecBackend = cfg.VectorStore.Backend
	}
	vecCount := vs.Count()

	var dbSize int64
	if fi, err := os.Stat(config.DBPath()); err == nil {
		dbSize = fi.Size()
	}

	type statusOut struct {
		Workspaces     int     `json:"workspaces"`
		Documents      int     `json:"documents"`
		Chunks         int     `json:"chunks"`
		Vectors        int     `json:"vectors"`
		VectorStore    string  `json:"vector_store"`
		DBSizeBytes    int64   `json:"db_size_bytes"`
		LastIndexed    string  `json:"last_indexed"`
		VectorParity   bool    `json:"vector_parity_ok"`
		VectorState    string  `json:"vector_state"`
		EmbedCoverage  float64 `json:"embed_coverage"`
		EmbedderStatus string  `json:"embedder_status"`
	}
	state, coverage := vectorParityState(vecCount, stats.ChunkCount)
	embedderStatus := "not configured"
	if cfg.HasEmbedder() {
		embedderStatus = fmt.Sprintf("%s / %s", cfg.Embedder.Provider, cfg.Embedder.Model)
	}
	out := statusOut{
		Workspaces:     stats.WorkspaceCount,
		Documents:      stats.DocumentCount,
		Chunks:         stats.ChunkCount,
		Vectors:        vecCount,
		VectorStore:    vecBackend,
		DBSizeBytes:    dbSize,
		LastIndexed:    lastIndexed,
		VectorParity:   state == "ok",
		VectorState:    state,
		EmbedCoverage:  coverage,
		EmbedderStatus: embedderStatus,
	}

	if flagJSON {
		PrintJSON(out)
		return nil
	}

	parity := vectorParityMessage(vecCount, stats.ChunkCount)

	embedderLine := embedderStatus
	if !cfg.HasEmbedder() {
		embedderLine = "not configured (keyword-only search)"
	}

	PrintTable(
		[]string{"Metric", "Value"},
		[][]string{
			{"Workspaces", fmt.Sprintf("%d", out.Workspaces)},
			{"Documents", fmt.Sprintf("%d", out.Documents)},
			{"Chunks", fmt.Sprintf("%d", out.Chunks)},
			{"Vectors", fmt.Sprintf("%d", out.Vectors)},
			{"Vector store", out.VectorStore},
			{"Vector parity", parity},
			{"Embedder", embedderLine},
			{"DB size", fmt.Sprintf("%.1f KB", float64(out.DBSizeBytes)/1024)},
			{"Last indexed", lastIndexed},
		},
	)
	return nil
}

// vectorParityState classifies the relationship between vector count and
// chunk count into one of three states: "ok" (equal), "embedding"
// (vectors < chunks — progressive embedding in progress), or "orphan"
// (vectors > chunks — stale vectors left over from re-indexing/deletes).
// coverage is vecCount/chunkCount in [0,1], reported as 1.0 when chunkCount
// is 0 (nothing to embed) or when there are orphan vectors.
func vectorParityState(vecCount, chunkCount int) (state string, coverage float64) {
	switch {
	case vecCount == chunkCount:
		return "ok", 1.0
	case vecCount < chunkCount:
		cov := 0.0
		if chunkCount > 0 {
			cov = float64(vecCount) / float64(chunkCount)
		}
		return "embedding", cov
	default:
		return "orphan", 1.0
	}
}

// vectorParityMessage renders the human-readable status line for the
// "Vector parity" row, mirroring vectorParityState's three cases.
func vectorParityMessage(vecCount, chunkCount int) string {
	state, coverage := vectorParityState(vecCount, chunkCount)
	switch state {
	case "ok":
		return "ok"
	case "embedding":
		return fmt.Sprintf("embedding in progress (%d/%d, %.0f%%)", vecCount, chunkCount, coverage*100)
	default: // "orphan"
		return fmt.Sprintf("orphan vectors (vectors=%d, chunks=%d)", vecCount, chunkCount)
	}
}
