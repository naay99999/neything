package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/config"
)

func applyProviderOverride(cfg *config.Config, forEmbedder bool) {
	if flagProvider == "" {
		return
	}
	if forEmbedder {
		cfg.Embedder.Provider = flagProvider
	} else {
		cfg.Chat.Provider = flagProvider
	}
}

func initAppFromConfig(cfg *config.Config) (*AppState, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	return initApp(cfg)
}
