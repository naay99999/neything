package main

import (
	"fmt"
	"strings"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/search"
	"github.com/naay99999/neything/internal/store"
)

// requireWorkspace turns a typo'd --workspace into a clear error instead of
// silently returning "No results found."
func requireWorkspace(db *store.DB, name string) error {
	if name == "" {
		return nil
	}
	ws, err := db.GetWorkspaceByName(name)
	if err != nil {
		return err
	}
	if ws != nil {
		return nil
	}
	all, err := db.ListWorkspaces()
	if err == nil && len(all) > 0 {
		names := make([]string, len(all))
		for i, w := range all {
			names[i] = w.Name
		}
		return fmt.Errorf("workspace %q not found (existing: %s)", name, strings.Join(names, ", "))
	}
	return fmt.Errorf("workspace %q not found — create it with: ney index <path> --workspace %s", name, name)
}

func retrieveOpts(db *store.DB, cfg *config.Config, topK int) search.RetrieveOptions {
	return search.RetrieveOptions{
		TopK:       topK,
		FetchK:     config.FetchK(cfg, topK),
		Workspace:  effectiveWorkspaceName(db),
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
