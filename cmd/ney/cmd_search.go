package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/naay99999/neything/internal/citation"
	"github.com/naay99999/neything/internal/scan"
	"github.com/naay99999/neything/internal/search"
	"github.com/naay99999/neything/internal/store"
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
	groups = appendLiveScan(cmd.Context(), app.DB, groups, query)

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
		if g.Source == "live-scan" {
			label += " " + Dim("(live scan — not yet indexed)")
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

// appendLiveScan runs a tier-0 filesystem scan (internal/scan) and appends
// its hits, deduped by path, when the CLI heuristic (design §6.1) decides
// the search scope isn't backed by an indexed workspace yet — e.g. a brand
// new folder that hasn't seen `ney index`. Unlike `ney mcp` (which knows
// exactly which roots are still on their initial Phase A scan via
// serverState), a one-shot `ney search` process has no such state to
// consult, so it falls back to liveScanRoot's path/document-count check.
// Best-effort and non-fatal: scan errors are swallowed since keyword/
// semantic search already ran and this is purely additive. Included in
// --json output too (tagged source: "live-scan"), not just interactive text.
func appendLiveScan(ctx context.Context, db *store.DB, groups []search.FileGroup, query string) []search.FileGroup {
	root := liveScanRoot(db, flagPath)
	if root == "" {
		return groups
	}
	hits, _, err := scan.Scan(ctx, root, query, scan.Options{})
	if err != nil || len(hits) == 0 {
		return groups
	}

	existing := make(map[string]bool, len(groups))
	for _, g := range groups {
		existing[g.DocPath] = true
	}
	for _, h := range hits {
		if existing[h.Path] {
			continue
		}
		existing[h.Path] = true
		snippet := h.Snippet
		if snippet == "" {
			snippet = "(filename match)"
		}
		groups = append(groups, search.FileGroup{
			DocPath:   h.Path,
			BestScore: float32(h.Score),
			Source:    "live-scan",
			Chunks: []search.EnrichedResult{{
				DocPath: h.Path,
				Content: snippet,
				Score:   float32(h.Score),
			}},
		})
	}
	return groups
}

// liveScanRoot returns the filesystem root a tier-0 scan should cover, or ""
// if a live scan shouldn't run at all — the search scope is already backed
// by an indexed workspace with documents in it. Per design §6.1's CLI
// heuristic: no workspace covers the search scope (--path, or cwd when
// unset), or the covering workspace has zero documents indexed yet.
// --all searches everything on purpose, so there's no single scope path a
// live scan could meaningfully cover — skipped entirely in that case.
func liveScanRoot(db *store.DB, pathFlag string) string {
	if flagAll {
		return ""
	}
	scope := pathFlag
	if scope == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		scope = cwd
	}
	abs, err := filepath.Abs(expandTilde(scope))
	if err != nil {
		return ""
	}

	var ws *store.Workspace
	if flagWorkspace != "" {
		ws, _ = db.GetWorkspaceByName(flagWorkspace)
	} else {
		ws = resolveWorkspaceForPath(db, abs)
	}
	if ws == nil {
		return abs
	}
	docs, err := db.GetDocumentsByWorkspace(ws.ID)
	if err == nil && len(docs) > 0 {
		return ""
	}
	return ws.RootPath
}
