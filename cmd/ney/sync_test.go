package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

// TestSyncWorkspaceIfKnownNilEmbedderNoPanic covers the panic path flagged in
// the design/plan docs: syncWorkspaceIfKnown (called from ask/search before
// every request) calls Indexer.Index with app.Embedder nil whenever no
// embedder is configured ("none", the new zero-config default). It must not
// panic, and must reindex the cwd workspace normally (FTS-only).
func TestSyncWorkspaceIfKnownNilEmbedderNoPanic(t *testing.T) {
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
	vs, err := vectorstore.NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer vs.Close()

	if _, err := db.UpsertWorkspace("corpus", root); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Chunking: config.ChunkingConfig{Strategy: "character", TargetChars: 200, OverlapChars: 20},
	}
	app := &AppState{
		Config:   cfg,
		DB:       db,
		Vectors:  vs,
		Embedder: nil, // no embedder configured — the scenario under test
	}

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
		t.Fatal("expected sync to index note.md via Phase-A-only (nil embedder) path")
	}
	if vs.Count() != 0 {
		t.Fatalf("expected empty vector store with nil embedder, got %d", vs.Count())
	}
}
