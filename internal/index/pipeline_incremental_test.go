package index

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

func assertVectorChunkParity(t *testing.T, db *store.DB, vs vectorstore.VectorStore) {
	t.Helper()
	chunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	chunkSet := make(map[string]bool, len(chunkIDs))
	for _, id := range chunkIDs {
		chunkSet[strconv.FormatInt(id, 10)] = true
	}
	if vs.Count() != len(chunkIDs) {
		t.Fatalf("vector/chunk count mismatch: vectors=%d chunks=%d", vs.Count(), len(chunkIDs))
	}
}

func TestIndexerReindexPreservesVectorCount(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "doc.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("version one content here"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	runEmbedWorker(t, ix)
	count1 := ix.Vectors.Count()

	if err := os.WriteFile(path, []byte("version two with different content"), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.VectorsPruned == 0 {
		t.Fatal("expected vectors pruned on re-index")
	}
	runEmbedWorker(t, ix)
	if ix.Vectors.Count() != count1 {
		t.Fatalf("expected vector count %d after re-index, got %d", count1, ix.Vectors.Count())
	}
	assertVectorChunkParity(t, db, ix.Vectors)
}

func TestIndexerHashSkip(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "doc.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stable content"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesSkipped != 1 {
		t.Fatalf("expected 1 skipped file, got %d", stats.FilesSkipped)
	}
	if stats.ChunksCreated != 0 {
		t.Fatalf("expected 0 chunks on skip pass, got %d", stats.ChunksCreated)
	}
}

func TestIndexerRemovesDeletedFile(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	keep := filepath.Join(root, "keep.md")
	remove := filepath.Join(root, "remove.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("keep this file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remove, []byte("remove this file"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	runEmbedWorker(t, ix)
	if err := os.Remove(remove); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("expected 1 file removed, got %d", stats.FilesRemoved)
	}
	doc, err := db.GetDocumentByPath(remove)
	if err != nil {
		t.Fatal(err)
	}
	if doc != nil {
		t.Fatal("expected removed document gone from db")
	}
	assertVectorChunkParity(t, db, ix.Vectors)
}

func TestIndexerRenameByHash(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	oldPath := filepath.Join(root, "old.md")
	newPath := filepath.Join(root, "new.md")
	content := []byte("same content for rename test")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	runEmbedWorker(t, ix)
	vectorCount := ix.Vectors.Count()
	if vectorCount == 0 {
		t.Fatal("expected vectors after embedding")
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 0 {
		t.Fatalf("expected rename not removal, files_removed=%d", stats.FilesRemoved)
	}
	if stats.ChunksCreated != 0 {
		t.Fatalf("expected no re-embed on rename, chunks_created=%d", stats.ChunksCreated)
	}

	doc, err := db.GetDocumentByPath(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("expected document at new path")
	}
	oldDoc, err := db.GetDocumentByPath(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if oldDoc != nil {
		t.Fatal("expected old path removed from db after rename")
	}
	if ix.Vectors.Count() != vectorCount {
		t.Fatalf("expected vector count unchanged on rename, got %d want %d", ix.Vectors.Count(), vectorCount)
	}
}

func TestIndexerRemovePath(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "doc.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("remove me"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	runEmbedWorker(t, ix)
	if ix.Vectors.Count() == 0 {
		t.Fatal("expected vectors after embedding")
	}

	stats, err := ix.RemovePath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("expected 1 file removed, got %d", stats.FilesRemoved)
	}
	if ix.Vectors.Count() != 0 {
		t.Fatalf("expected 0 vectors, got %d", ix.Vectors.Count())
	}
}
