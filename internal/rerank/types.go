package rerank

import (
	"context"

	"github.com/naay99999/neything/internal/vectorstore"
)

type Reranker interface {
	Rerank(ctx context.Context, query string, results []vectorstore.SearchResult) []vectorstore.SearchResult
}
