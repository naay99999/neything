package search

import (
	"context"
	"fmt"
	"strconv"

	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/rerank"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
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

type RetrieveOptions struct {
	TopK         int
	FetchK       int
	Workspace    string
	PathPrefix   string
	Hybrid       bool
	Rerank       bool
}

type Retriever struct {
	DB       *store.DB
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder
	Reranker rerank.Reranker
}

func (r *Retriever) Search(ctx context.Context, query string, opts RetrieveOptions) ([]EnrichedResult, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 8
	}
	fetchK := opts.FetchK
	if fetchK <= 0 {
		fetchK = topK * 3
	}
	if fetchK < 10 {
		fetchK = 10
	}

	semantic, err := r.semanticSearch(ctx, query, fetchK, opts.Workspace, opts.PathPrefix)
	if err != nil {
		return nil, err
	}

	var results []EnrichedResult
	if opts.Hybrid {
		keyword, err := r.keywordSearch(ctx, query, fetchK, opts.Workspace, opts.PathPrefix)
		if err != nil {
			return nil, err
		}
		results = ReciprocalRankFusion(semantic, keyword, defaultRRFK)
	} else {
		results = semantic
	}

	if opts.Rerank && r.Reranker != nil && len(results) > 0 {
		byID := make(map[int64]EnrichedResult, len(results))
		for _, res := range results {
			byID[res.ChunkID] = res
		}

		candidates := make([]rerank.Candidate, len(results))
		for i, res := range results {
			candidates[i] = rerank.Candidate{
				ChunkID: res.ChunkID,
				Content: res.Content,
				Score:   res.Score,
			}
		}
		ranked, err := r.Reranker.Rerank(ctx, query, candidates)
		if err != nil {
			return nil, fmt.Errorf("rerank: %w", err)
		}
		results = make([]EnrichedResult, len(ranked))
		for i, c := range ranked {
			if orig, ok := byID[c.ChunkID]; ok {
				orig.Score = c.Score
				results[i] = orig
			} else {
				results[i] = EnrichedResult{
					ChunkID: c.ChunkID,
					Content: c.Content,
					Score:   c.Score,
				}
			}
		}
	}

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func (r *Retriever) semanticSearch(ctx context.Context, query string, fetchK int, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	vecs, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	rawResults, err := r.Vectors.Search(ctx, vecs[0], fetchK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

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

	return r.enrichResults(chunkIDs, scoreByID, workspaceName, pathPrefix)
}

func (r *Retriever) keywordSearch(_ context.Context, query string, fetchK int, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	ftsResults, err := r.DB.SearchFTS(query, fetchK)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	if len(ftsResults) == 0 {
		return nil, nil
	}

	chunkIDs := make([]int64, len(ftsResults))
	scoreByID := make(map[int64]float32, len(ftsResults))
	for i, fr := range ftsResults {
		chunkIDs[i] = fr.ChunkID
		scoreByID[fr.ChunkID] = fr.Score
	}

	return r.enrichResults(chunkIDs, scoreByID, workspaceName, pathPrefix)
}

func (r *Retriever) enrichResults(chunkIDs []int64, scoreByID map[int64]float32, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}

	docMap, err := r.DB.GetDocumentsByChunkIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch docs: %w", err)
	}

	chunks, err := r.DB.GetChunksByIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: %w", err)
	}

	chunkByID := make(map[int64]*store.Chunk, len(chunks))
	for _, c := range chunks {
		chunkByID[c.ID] = c
	}

	var results []EnrichedResult
	for _, id := range chunkIDs {
		c, ok := chunkByID[id]
		if !ok {
			continue
		}
		dw, ok := docMap[c.ID]
		if !ok {
			continue
		}
		if workspaceName != "" && dw.WorkspaceName != workspaceName {
			continue
		}
		if pathPrefix != "" && !hasPathPrefix(dw.Path, pathPrefix) {
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

	return results, nil
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}
