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

func initApp(cfg *config.Config) (*AppState, error) {
	return initAppWithOptions(cfg, false)
}

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
	chatModel, err := config.NewChatModel(cfg)
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
		Chat:     chatModel,
		Reranker: reranker,
	}, nil
}
