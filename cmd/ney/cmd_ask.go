package main

import (
	"fmt"
	"strings"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/citation"
	"github.com/naay99999/neything/internal/search"
	"github.com/spf13/cobra"
)

var askCmd = &cobra.Command{
	Use:   "ask \"<question>\"",
	Short: "Ask a question and get an answer with sources",
	Args:  cobra.ExactArgs(1),
	RunE:  runAsk,
}

func runAsk(cmd *cobra.Command, args []string) error {
	question := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	applyProviderOverride(cfg, false)
	app, err := initAppFromConfig(cfg, true)
	if err != nil {
		return err
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	if app.Chat == nil {
		return fmt.Errorf("no chat provider configured — run `ney init` to set one up")
	}

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
	results, meta, err := newRetriever(app).Search(cmd.Context(), question, opts)
	sp.Stop()
	if err != nil {
		return err
	}
	if len(results) == 0 && opts.Workspace != "" {
		globalOpts := opts
		globalOpts.Workspace = ""
		if fallback, fmeta, ferr := newRetriever(app).Search(cmd.Context(), question, globalOpts); ferr == nil && len(fallback) > 0 {
			results = fallback
			meta = fmeta
			fmt.Println(Dim("(nothing in this folder — showing results from other indexed workspaces)"))
		}
	}
	if !flagJSON {
		printDegradationNote(meta)
	}
	if len(results) == 0 {
		return fmt.Errorf("no relevant context found — try indexing more files")
	}

	var ctxChunks []chunk.Chunk
	totalChars := 0
	for _, r := range results {
		if totalChars+len(r.Content) > cfg.Retrieval.MaxContextChars {
			break
		}
		ctxChunks = append(ctxChunks, chunk.Chunk{
			ID:       fmt.Sprintf("%d", r.ChunkID),
			DocPath:  r.DocPath,
			DocType:  r.DocType,
			Content:  r.Content,
			StartPos: r.StartPos,
			EndPos:   r.EndPos,
		})
		totalChars += len(r.Content)
	}

	sp = startSpinner("thinking")
	answer, err := app.Chat.Complete(cmd.Context(), question, ctxChunks)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("LLM error: %w", err)
	}

	if flagJSON {
		type jsonAnswer struct {
			Answer  string                  `json:"answer"`
			Sources []search.EnrichedResult `json:"sources"`
			Meta    search.SearchMeta       `json:"meta"`
		}
		PrintJSON(jsonAnswer{Answer: answer, Sources: results, Meta: meta})
		return nil
	}

	typewrite(answer)

	if !strings.Contains(strings.ToLower(answer), "sources") {
		fmt.Println(Dim("\nSources:"))
		for _, src := range dedupeSources(results) {
			fmt.Printf("  %s\n", Bold(src))
		}
	}
	return nil
}

func dedupeSources(results []search.EnrichedResult) []string {
	mixed := mixedWorkspaces(results)
	seen := make(map[string]bool)
	var out []string
	for _, r := range results {
		src := citation.FormatSource(displayPath(r.DocPath), r.DocType, r.StartPos, r.EndPos)
		if mixed && r.Workspace != "" {
			src = fmt.Sprintf("[%s] %s", r.Workspace, src)
		}
		if seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	return out
}

// mixedWorkspaces reports whether results span more than one distinct
// workspace — true when --all/:all was used or a fallback jumped scope.
func mixedWorkspaces(results []search.EnrichedResult) bool {
	seen := make(map[string]bool)
	for _, r := range results {
		if r.Workspace != "" {
			seen[r.Workspace] = true
		}
	}
	return len(seen) > 1
}
