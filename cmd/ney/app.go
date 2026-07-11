package main

import (
	"github.com/naay99999/neything/internal/chat"
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
	Chat     chat.ChatModel
	Reranker rerank.Reranker
}

// initAppWithOptions wires the app. needChat is false for commands that never
// call the LLM (index/search/watch) so a missing chat API key can't block them.
//
// A nil AppState.Embedder or AppState.Chat is a normal, expected state when
// the corresponding provider is unset ("none") in config — config.NewEmbedder
// / config.NewChatModel return (nil, nil) in that case, not an error.
// Commands that need one (e.g. `ask`) check for nil themselves and print a
// friendly hint. An explicit --provider override (applyProviderOverride)
// still sets a concrete provider name, so a build failure for it (bad key,
// unreachable endpoint) surfaces as an error here as before.
func initAppWithOptions(cfg *config.Config, migrateVectors, needChat bool) (*AppState, error) {
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
	var chatModel chat.ChatModel
	if needChat {
		chatModel, err = config.NewChatModel(cfg)
		if err != nil {
			db.Close()
			return nil, err
		}
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
		Chat:     chatModel,
		Reranker: reranker,
	}, nil
}
