package main

import (
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/search"
)

func retrieveOpts(cfg *config.Config, topK int) search.RetrieveOptions {
	return search.RetrieveOptions{
		TopK:       topK,
		FetchK:     config.FetchK(cfg, topK),
		Workspace:  flagWorkspace,
		PathPrefix: flagPath,
		Hybrid:     cfg.Retrieval.Hybrid,
		Rerank:     cfg.Retrieval.Rerank,
	}
}

func newRetriever(app *AppState) *search.Retriever {
	return &search.Retriever{
		DB:       app.DB,
		Vectors:  app.Vectors,
		Embedder: app.Embedder,
		Reranker: app.Reranker,
	}
}
