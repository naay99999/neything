package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
)

// TestSyncWorkspaceIfKnownIndexesCwdWorkspace covers the pre-search sync:
// `ney search` silently re-indexes the workspace containing the cwd so edits
// made outside ney are picked up, without failing the caller's request.
func TestSyncWorkspaceIfKnownIndexesCwdWorkspace(t *testing.T) {
	// syncWorkspaceIfKnown now takes the writer lock at config.NeyDir()
	// (~/.ney); isolate it from the real home directory like other tests
	// that touch config paths (see cmd_ask_test.go).
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	root := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello from the sync test"), 0644); err != nil {
		t.Fatal(err)
	}
	// Resolve symlinks (macOS's /var -> /private/var) up front so the
	// workspace's root_path matches what os.Getwd() reports after Chdir —
	// cwdWorkspace() compares against the resolved cwd.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = resolvedRoot

	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.UpsertWorkspace("corpus", root); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Chunking: config.ChunkingConfig{Strategy: "character", TargetChars: 200, OverlapChars: 20},
	}
	app := &AppState{Config: cfg, DB: db}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origWD)

	origWorkspace, origAll := flagWorkspace, flagAll
	flagWorkspace, flagAll = "", false
	defer func() { flagWorkspace, flagAll = origWorkspace, origAll }()

	// Must not panic.
	syncWorkspaceIfKnown(context.Background(), app, cfg)

	doc, err := db.GetDocumentByPath(filepath.Join(root, "note.md"))
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("expected sync to index note.md")
	}
}
