package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

// setupIndexerNilEmbedder mirrors setupIndexer but leaves Embedder nil,
// exercising the FTS-only (tier 0-1) indexing path.
func setupIndexerNilEmbedder(t *testing.T) (*Indexer, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	vecPath := filepath.Join(dir, "vectors.bin")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := vectorstore.NewBruteForceStore(vecPath)
	if err != nil {
		t.Fatal(err)
	}
	chunkResolver, err := chunk.NewResolver("character", 200, 20, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{
		DB:            db,
		Vectors:       vs,
		Embedder:      nil,
		Loaders:       loader.NewRegistry(&loader.MarkdownLoader{}),
		ChunkResolver: chunkResolver,
		BatchSize:     8,
	}
	return ix, db, dir
}

// TestIndexerNilEmbedderIndexesFTSOnly is the scenario called out in the
// design doc (§4.1) and plan (M1.2): an Indexer with a nil Embedder must
// still parse/chunk/FTS-index documents (tier 0-1), just skip embedding.
// It also covers the known panic path from cmd/ney/sync.go calling Index()
// via syncWorkspaceIfKnown with a nil embedder.
func TestIndexerNilEmbedderIndexesFTSOnly(t *testing.T) {
	ix, db, dir := setupIndexerNilEmbedder(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "billing.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	unique := "billing uses stripe subscriptions for recurring revenue"
	if err := os.WriteFile(path, []byte(unique+"\nextra notes"), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatalf("Index with nil embedder panicked or errored: %v", err)
	}
	if stats.FilesScanned != 1 {
		t.Fatalf("expected 1 file scanned, got %d", stats.FilesScanned)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunk rows to be created even without an embedder")
	}

	// Chunk rows exist.
	doc, err := db.GetDocumentByPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("expected document row to exist")
	}
	if doc.Hash == "" {
		t.Fatal("expected document hash to be recorded")
	}
	chunkIDs, err := db.GetChunkIDsByDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunkIDs) == 0 {
		t.Fatal("expected chunk rows for document")
	}

	// FTS rows exist and are searchable.
	ftsResults, err := db.SearchFTS("stripe", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ftsResults) == 0 {
		t.Fatal("expected FTS search to find indexed content")
	}

	// Vector store stays empty — nothing was embedded.
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected empty vector store with nil embedder, got %d vectors", got)
	}

	// Re-running the same content is skipped via the hash check (same as
	// the embedder path) — no panic, no duplicate work.
	stats2, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatalf("re-index with nil embedder errored: %v", err)
	}
	if stats2.FilesSkipped != 1 {
		t.Fatalf("expected 1 skipped file on re-run, got %d", stats2.FilesSkipped)
	}
	if stats2.ChunksCreated != 0 {
		t.Fatalf("expected 0 new chunks on unchanged re-run, got %d", stats2.ChunksCreated)
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected vector store to remain empty after re-run, got %d", got)
	}
}

// TestIndexerNilEmbedderIndexPathNoPanic covers IndexPath (used for
// single-file re-index, e.g. by the watcher) with a nil embedder.
func TestIndexerNilEmbedderIndexPathNoPanic(t *testing.T) {
	ix, db, dir := setupIndexerNilEmbedder(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "note.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("a lone note about widgets"), 0644); err != nil {
		t.Fatal(err)
	}

	wsID, err := db.UpsertWorkspace("test", root)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ix.IndexPath(context.Background(), path, wsID, "test")
	if err != nil {
		t.Fatalf("IndexPath with nil embedder panicked or errored: %v", err)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created for IndexPath with nil embedder")
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected empty vector store, got %d", got)
	}
}

// TestIndexerNilEmbedderRemovePathNoPanic covers RemovePath (which never
// touches Embedder) still working normally alongside a nil-embedder Indexer.
func TestIndexerNilEmbedderRemovePathNoPanic(t *testing.T) {
	ix, db, dir := setupIndexerNilEmbedder(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "doc.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("remove me please"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.RemovePath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("expected 1 file removed, got %d", stats.FilesRemoved)
	}
}
