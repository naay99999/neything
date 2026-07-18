package main

import (
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/rerank"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

type AppState struct {
	Config   *config.Config
	DB       *store.DB
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder
	Reranker rerank.Reranker
}

// initAppWithOptions wires the app.
//
// A nil AppState.Embedder is a normal, expected state when the embedder
// provider is unset ("none") in config — config.NewEmbedder returns
// (nil, nil) in that case, not an error; search degrades to keyword-only.
// An explicit --provider override (applyProviderOverride) still sets a
// concrete provider name, so a build failure for it (bad key, unreachable
// endpoint) surfaces as an error here as before.
func initAppWithOptions(cfg *config.Config, migrateVectors bool) (*AppState, error) {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, err
	}
	vs, err := config.NewVectorStore(cfg, db, migrateVectors)
	if err != nil {
		db.Close()
		return nil, err
	}
	emb, err := config.NewEmbedder(cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	reranker, err := config.NewReranker(cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &AppState{
		Config:   cfg,
		DB:       db,
		Vectors:  vs,
		Embedder: emb,
		Reranker: reranker,
	}, nil
}
