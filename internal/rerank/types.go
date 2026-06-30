package rerank

import "context"

type Candidate struct {
	ChunkID int64
	Content string
	Score   float32
}

type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error)
	ModelID() string
}
