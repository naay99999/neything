package vectorstore

import "context"

type VectorItem struct {
	ID     string
	Vector []float32
}

type SearchResult struct {
	ID    string
	Score float32
}

type VectorStore interface {
	Add(ctx context.Context, items []VectorItem) error
	Search(ctx context.Context, query []float32, k int) ([]SearchResult, error)
	Delete(ctx context.Context, ids []string) error
	Count() int
	Close() error
}
