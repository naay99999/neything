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
	app, err := initAppFromConfig(cfg)
	if err != nil {
		return err
	}
	defer app.DB.Close()

	topK := flagTopK
	if topK <= 0 {
		topK = cfg.Retrieval.TopK
	}

	results, err := newRetriever(app).Search(cmd.Context(), question, retrieveOpts(cfg, topK))
	if err != nil {
		return err
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

	answer, err := app.Chat.Complete(cmd.Context(), question, ctxChunks)
	if err != nil {
		return fmt.Errorf("LLM error: %w", err)
	}

	if flagJSON {
		type jsonAnswer struct {
			Answer  string                  `json:"answer"`
			Sources []search.EnrichedResult `json:"sources"`
		}
		PrintJSON(jsonAnswer{Answer: answer, Sources: results})
		return nil
	}

	fmt.Println(answer)

	if !strings.Contains(strings.ToLower(answer), "sources") {
		fmt.Println("\nSources:")
		for _, src := range dedupeSources(results) {
			fmt.Printf("  %s\n", src)
		}
	}
	return nil
}

func dedupeSources(results []search.EnrichedResult) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range results {
		src := citation.FormatSource(r.DocPath, r.DocType, r.StartPos, r.EndPos)
		if seen[src] {
			continue
		}
		seen[src] = true
		out = append(out, src)
	}
	return out
}
