package main

import (
	"fmt"
	"os"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/pathfilter"
)

// newLoaderRegistry builds the same loader set every indexing path uses
// (order matters for .md sniffing — see CLAUDE.md). Factored out of
// newIndexer so `ney mcp`'s read_document tool can reuse it for fresh-parse
// of not-yet-indexed files without duplicating the loader list.
func newLoaderRegistry(_ *config.Config) loader.Registry {
	return loader.NewRegistry(
		&loader.NotionLoader{},
		&loader.ObsidianLoader{},
		&loader.MarkdownLoader{},
		&loader.TextLoader{},
	)
}

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

	return &index.Indexer{
		DB:            app.DB,
		Loaders:       newLoaderRegistry(cfg),
		Filter:        newPathFilter(cfg),
		ChunkResolver: chunkResolver,
		BatchSize:     32,
		OnProgress: func(file string, chunks int) {
			fmt.Fprintf(os.Stderr, "  indexed %s (%d chunks)\n", displayPath(file), chunks)
		},
	}, nil
}

// newPathFilter builds the shared secret/exclude filter from config. Config
// validation already rejected malformed globs, so an error here (only
// possible if cfg bypassed Validate) falls back to built-in rules via nil.
func newPathFilter(cfg *config.Config) *pathfilter.Filter {
	flt, err := pathfilter.New(cfg.Index.Exclude)
	if err != nil {
		return nil
	}
	return flt
}
