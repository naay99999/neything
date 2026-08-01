package main

import (
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
)

type AppState struct {
	Config *config.Config
	DB     *store.DB
}

// initApp opens the one resource every command shares: the SQLite index.
func initApp(cfg *config.Config) (*AppState, error) {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return nil, err
	}
	return &AppState{Config: cfg, DB: db}, nil
}
