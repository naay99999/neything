package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/citation"
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
	applyProviderOverride(cfg, true)
	app, err := initAppFromConfig(cfg)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	topK := flagTopK
	if topK <= 0 {
		topK = cfg.Retrieval.TopK
	}

	sp := startSpinner("searching")
	results, err := newRetriever(app).Search(cmd.Context(), query, retrieveOpts(cfg, topK))
	sp.Stop()
	if err != nil {
		return err
	}

	groups := search.GroupByFile(results)

	if flagJSON {
		PrintJSON(search.GroupedResults{Files: groups})
		return nil
	}

	if len(groups) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	for _, g := range groups {
		fmt.Printf("%s %s\n", Bold(g.DocPath), Dim(fmt.Sprintf("(best: %.4f)", g.BestScore)))
		for i, r := range g.Chunks {
			loc := citation.FormatLocation(r.DocType, r.StartPos, r.EndPos)
			if loc != "" {
				fmt.Printf("  [%d] %s  %s\n", i+1, loc, Dim(fmt.Sprintf("score: %.4f", r.Score)))
			} else {
				fmt.Printf("  [%d] %s\n", i+1, Dim(fmt.Sprintf("score: %.4f", r.Score)))
			}
			fmt.Printf("    %s\n\n", truncate(r.Content, 200))
		}
	}
	return nil
}
