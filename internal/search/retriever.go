package search

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/naay/ney/internal/embed"
	"github.com/naay/ney/internal/store"
	"github.com/naay/ney/internal/vectorstore"
)

type EnrichedResult struct {
	ChunkID   int64
	DocPath   string
	DocType   string
	Content   string
	StartPos  int
	EndPos    int
	Score     float32
	Workspace string
}

type Retriever struct {
	DB       *store.DB
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder
}

func (r *Retriever) Search(ctx context.Context, query string, topK int, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	vecs, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	fetchK := topK * 3
	if fetchK < 10 {
		fetchK = 10
	}
	rawResults, err := r.Vectors.Search(ctx, vecs[0], fetchK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// collect chunk IDs
	chunkIDs := make([]int64, 0, len(rawResults))
	scoreByID := make(map[int64]float32, len(rawResults))
	for _, res := range rawResults {
		id, err := strconv.ParseInt(res.ID, 10, 64)
		if err != nil {
			continue
		}
		chunkIDs = append(chunkIDs, id)
		scoreByID[id] = res.Score
	}

	if len(chunkIDs) == 0 {
		return nil, nil
	}

	// fetch chunks + docs in one JOIN query
	docMap, err := r.DB.GetDocumentsByChunkIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch docs: %w", err)
	}

	chunks, err := r.DB.GetChunksByIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: %w", err)
	}

	// build results
	var results []EnrichedResult
	for _, c := range chunks {
		dw, ok := docMap[c.ID]
		if !ok {
			continue
		}
		if workspaceName != "" && dw.WorkspaceName != workspaceName {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(dw.Path, pathPrefix) {
			continue
		}
		results = append(results, EnrichedResult{
			ChunkID:   c.ID,
			DocPath:   dw.Path,
			DocType:   dw.Type,
			Content:   c.Content,
			StartPos:  c.StartPos,
			EndPos:    c.EndPos,
			Score:     scoreByID[c.ID],
			Workspace: dw.WorkspaceName,
		})
	}

	// sort by score descending
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}
