package main

import (
	"fmt"
	"os"
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
		Mode:       cfg.Retrieval.Mode,
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

// printDegradationNote writes a short stderr note when auto/hybrid mode
// couldn't use every signal it wanted (embedder down, no vectors yet, no
// embedder configured at all), so the user understands why results look
// keyword-only without the command failing outright. A no-op when nothing
// was degraded.
func printDegradationNote(meta search.SearchMeta) {
	if meta.Degraded == "" {
		return
	}
	if strings.Contains(meta.Degraded, "no embedder configured") {
		fmt.Fprintln(os.Stderr, Dim("note: keyword-only search — run `ney init` to enable semantic search"))
		return
	}
	fmt.Fprintln(os.Stderr, Dim("note: "+meta.Degraded))
}
