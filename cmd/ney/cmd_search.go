package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/citation"
	"github.com/naay99999/neything/internal/search"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search \"<query>\"",
	Short: "Search indexed files (semantic + keyword, auto by default)",
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
	app, err := initAppFromConfig(cfg, false)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	if err := requireWorkspace(app.DB, flagWorkspace); err != nil {
		return err
	}
	syncWorkspaceIfKnown(cmd.Context(), app, cfg)

	topK := flagTopK
	if topK <= 0 {
		topK = cfg.Retrieval.TopK
	}

	opts := retrieveOpts(app.DB, cfg, topK)
	sp := startSpinner("searching")
	results, meta, err := newRetriever(app).Search(cmd.Context(), query, opts)
	sp.Stop()
	if err != nil {
		return err
	}
	fellBack := false
	if len(results) == 0 && opts.Workspace != "" {
		globalOpts := opts
		globalOpts.Workspace = ""
		if fallback, fmeta, ferr := newRetriever(app).Search(cmd.Context(), query, globalOpts); ferr == nil && len(fallback) > 0 {
			results = fallback
			meta = fmeta
			fellBack = true
		}
	}

	groups := search.GroupByFile(results)

	if flagJSON {
		PrintJSON(search.GroupedResults{Files: groups, Meta: &meta})
		return nil
	}

	printDegradationNote(meta)

	if len(groups) == 0 {
		fmt.Println("No results found.")
		return nil
	}
	if fellBack {
		fmt.Println(Dim("(nothing in this folder — showing results from other indexed workspaces)"))
	}

	mixed := mixedWorkspaces(results)
	for _, g := range groups {
		label := ""
		if mixed && g.Workspace != "" {
			label = " " + Dim("["+g.Workspace+"]")
		}
		fmt.Printf("%s%s %s\n", Bold(displayPath(g.DocPath)), label, Dim(fmt.Sprintf("(best: %.4f)", g.BestScore)))
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
