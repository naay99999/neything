package rerank

import (
	"context"

	"github.com/naay/ney/internal/vectorstore"
)

type Reranker interface {
	Rerank(ctx context.Context, query string, results []vectorstore.SearchResult) []vectorstore.SearchResult
}
