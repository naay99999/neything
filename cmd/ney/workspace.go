package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/naay99999/neything/internal/store"
)

// resolveWorkspaceForPath returns the most specific (longest root_path)
// workspace that contains path, or nil if none matches.
func resolveWorkspaceForPath(db *store.DB, path string) *store.Workspace {
	all, err := db.ListWorkspaces()
	if err != nil {
		return nil
	}
	var best *store.Workspace
	for _, ws := range all {
		if path != ws.RootPath && !strings.HasPrefix(path, ws.RootPath+string(filepath.Separator)) {
			continue
		}
		if best == nil || len(ws.RootPath) > len(best.RootPath) {
			best = ws
		}
	}
	return best
}

// cwdWorkspace resolves the workspace matching the current working
// directory, if any.
func cwdWorkspace(db *store.DB) *store.Workspace {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	return resolveWorkspaceForPath(db, absCwd)
}

// effectiveWorkspaceName picks the workspace scope for a search: an explicit
// --workspace wins, --all forces global ("") search, otherwise the workspace
// bound to the current directory (if any) is used. Returns "" when nothing
// matches, which falls back to today's unscoped (all-workspace) search.
func effectiveWorkspaceName(db *store.DB) string {
	if flagWorkspace != "" {
		return flagWorkspace
	}
	if flagAll {
		return ""
	}
	if ws := cwdWorkspace(db); ws != nil {
		return ws.Name
	}
	return ""
}
