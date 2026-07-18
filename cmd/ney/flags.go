package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/config"
)

func applyProviderOverride(cfg *config.Config) {
	if flagProvider == "" {
		return
	}
	cfg.Embedder.Provider = flagProvider
}

func initAppFromConfig(cfg *config.Config) (*AppState, error) {
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	return initAppWithOptions(cfg, false)
}
