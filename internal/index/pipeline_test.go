package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/search"
	"github.com/naay99999/neything/internal/store"
)

func setupIndexer(t *testing.T) (*Indexer, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	chunkResolver, err := chunk.NewResolver("character", 200, 20, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{
		DB:            db,
		Loaders:       loader.NewRegistry(&loader.MarkdownLoader{}),
		ChunkResolver: chunkResolver,
		BatchSize:     8,
	}
	return ix, db, dir
}

func TestIndexerIndexAndSearch(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	unique := "billing uses stripe subscriptions for recurring revenue"
	if err := os.WriteFile(filepath.Join(root, "billing.md"), []byte(unique+"\nextra notes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.md"), []byte("unrelated gardening tips"), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 2 {
		t.Fatalf("expected 2 files scanned, got %d", stats.FilesScanned)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}

	retriever := &search.Retriever{DB: db}
	results, _, err := retriever.Search(context.Background(), unique, search.RetrieveOptions{
		TopK:      3,
		FetchK:    10,
		Workspace: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].DocPath != filepath.Join(root, "billing.md") {
		t.Fatalf("expected billing.md top result, got %s", results[0].DocPath)
	}
}
