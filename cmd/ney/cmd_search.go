package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/search"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search \"<query>\"",
	Short: "Semantic search over indexed files",
	Args:  cobra.ExactArgs(1),
	RunE:  runSearch,
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	app, err := initApp(cfg)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	topK := flagTopK
	if topK <= 0 {
		topK = cfg.Retrieval.TopK
	}

	retriever := &search.Retriever{
		DB:       app.DB,
		Vectors:  app.Vectors,
		Embedder: app.Embedder,
	}

	results, err := retriever.Search(cmd.Context(), query, topK, flagWorkspace, flagPath)
	if err != nil {
		return err
	}

	if flagJSON {
		PrintJSON(results)
		return nil
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for i, r := range results {
		fmt.Printf("[%d] %s", i+1, r.DocPath)
		if r.StartPos > 0 || r.EndPos > 0 {
			fmt.Printf(" (lines %d-%d)", r.StartPos, r.EndPos)
		}
		fmt.Printf("  score: %.4f\n", r.Score)
		fmt.Printf("    %s\n\n", truncate(r.Content, 200))
	}
	return nil
}
