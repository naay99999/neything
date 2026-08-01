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
		Workspace:  effectiveWorkspaceName(db),
		PathPrefix: flagPath,
	}
}

func newRetriever(app *AppState) *search.Retriever {
	return &search.Retriever{DB: app.DB}
}

// printSearchNote writes a short stderr note when the keyword search itself
// degraded (a failed FTS query), so the user understands why results look
// thin without the command failing outright. A no-op otherwise.
func printSearchNote(meta search.SearchMeta) {
	if meta.Degraded == "" {
		return
	}
	fmt.Fprintln(os.Stderr, Dim("note: "+meta.Degraded))
}
