package main

import (
	"fmt"
	"strings"

	"github.com/naay/ney/internal/chunk"
	"github.com/naay/ney/internal/search"
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
	if flagProvider != "" {
		cfg.Chat.Provider = flagProvider
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

	results, err := retriever.Search(cmd.Context(), question, topK, flagWorkspace, flagPath)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("no relevant context found — try indexing more files")
	}

	// convert to chunks, trimming to max_context_chars
	var ctxChunks []chunk.Chunk
	totalChars := 0
	for _, r := range results {
		if totalChars+len(r.Content) > cfg.Retrieval.MaxContextChars {
			break
		}
		ctxChunks = append(ctxChunks, chunk.Chunk{
			ID:       fmt.Sprintf("%d", r.ChunkID),
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
			Answer  string           `json:"answer"`
			Sources []search.EnrichedResult `json:"sources"`
		}
		PrintJSON(jsonAnswer{Answer: answer, Sources: results})
		return nil
	}

	fmt.Println(answer)

	// if the LLM didn't include a Sources section, append our own
	if !strings.Contains(strings.ToLower(answer), "sources") {
		fmt.Println("\nSources:")
		for _, r := range results {
			if r.StartPos > 0 {
				fmt.Printf("  %s (lines %d-%d)\n", r.DocPath, r.StartPos, r.EndPos)
			} else {
				fmt.Printf("  %s\n", r.DocPath)
			}
		}
	}
	return nil
}
