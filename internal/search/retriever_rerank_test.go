package search

import (
	"context"
	"testing"

	"github.com/naay99999/neything/internal/rerank"
)

type fakeReranker struct {
	order []int64
}

func (f *fakeReranker) ModelID() string { return "fake" }

func (f *fakeReranker) Rerank(_ context.Context, _ string, candidates []rerank.Candidate) ([]rerank.Candidate, error) {
	out := make([]rerank.Candidate, len(f.order))
	byID := make(map[int64]rerank.Candidate, len(candidates))
	for _, c := range candidates {
		byID[c.ChunkID] = c
	}
	for i, id := range f.order {
		c := byID[id]
		c.Score = float32(len(f.order) - i)
		out[i] = c
	}
	return out, nil
}

func TestRetriever_RerankReordersResults(t *testing.T) {
	r := &Retriever{
		Reranker: &fakeReranker{order: []int64{2, 1}},
	}

	results := []EnrichedResult{
		{ChunkID: 1, Content: "a", Score: 0.9},
		{ChunkID: 2, Content: "b", Score: 0.1},
	}

	byID := make(map[int64]EnrichedResult, len(results))
	for _, res := range results {
		byID[res.ChunkID] = res
	}
	candidates := make([]rerank.Candidate, len(results))
	for i, res := range results {
		candidates[i] = rerank.Candidate{ChunkID: res.ChunkID, Content: res.Content, Score: res.Score}
	}

	ranked, err := r.Reranker.Rerank(context.Background(), "q", candidates)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]EnrichedResult, len(ranked))
	for i, c := range ranked {
		orig := byID[c.ChunkID]
		orig.Score = c.Score
		out[i] = orig
	}

	if out[0].ChunkID != 2 {
		t.Fatalf("expected chunk 2 first after rerank, got %d", out[0].ChunkID)
	}
}
