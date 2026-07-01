package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/loader"
)

func newIndexer(app *AppState, cfg *config.Config) (*index.Indexer, error) {
	chunkResolver, err := chunk.NewResolver(
		cfg.Chunking.Strategy,
		cfg.Chunking.TargetChars,
		cfg.Chunking.OverlapChars,
		cfg.Chunking.TargetTokens,
		cfg.Chunking.OverlapTokens,
		cfg.Chunking.ByFormat,
	)
	if err != nil {
		return nil, err
	}

	ocrCfg := loader.OCRConfig{
		Enabled:      cfg.Loaders.OCR.Enabled,
		Lang:         cfg.Loaders.OCR.Lang,
		TesseractCmd: cfg.Loaders.OCR.TesseractCmd,
		PdftoppmCmd:  cfg.Loaders.OCR.PdftoppmCmd,
		MinChars:     cfg.Loaders.OCR.MinChars,
	}

	reg := loader.NewRegistry(
		&loader.NotionLoader{},
		&loader.ObsidianLoader{},
		&loader.MarkdownLoader{},
		&loader.HTMLLoader{},
		&loader.JSONLoader{},
		&loader.ConfluenceLoader{},
		&loader.PDFLoader{OCR: loader.NewOCRRunner(ocrCfg, nil)},
		&loader.DOCXLoader{},
	)

	var gitHistory *loader.GitHistoryLoader
	if cfg.Loaders.Git.RecentCommits > 0 {
		gitHistory = loader.NewGitHistoryLoader(cfg.Loaders.Git.RecentCommits)
	}

	return &index.Indexer{
		DB:            app.DB,
		Vectors:       app.Vectors,
		Embedder:      app.Embedder,
		Loaders:       reg,
		GitHistory:    gitHistory,
		ChunkResolver: chunkResolver,
		BatchSize:     32,
		OnProgress: func(file string, chunks int) {
			fmt.Fprintf(os.Stderr, "  indexed %s (%d chunks)\n", displayPath(file), chunks)
		},
	}, nil
}
