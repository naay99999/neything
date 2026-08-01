package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/store"
)

// ftsHits reports how many FTS rows match query.
func ftsHits(t *testing.T, db *store.DB, query string) int {
	t.Helper()
	res, err := db.SearchFTS(query, 50)
	if err != nil {
		t.Fatal(err)
	}
	return len(res)
}

// TestIndexerReindexReplacesOldChunks is the guard for the chunk+FTS cleanup
// that DeleteChunksByDocument owns. Its return value has no consumer any more
// (it used to feed VectorStore.Delete), so it would be easy to drop the call
// entirely — that leaves the OLD text searchable forever, silently, with no
// error anywhere.
func TestIndexerReindexReplacesOldChunks(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	path := filepath.Join(root, "doc.md")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("aardvark version one content here"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	if ftsHits(t, db, "aardvark") == 0 {
		t.Fatal("expected the original content to be FTS-searchable")
	}
	chunks1, err := db.CountChunks()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("bumblebee version two with different content"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	if n := ftsHits(t, db, "aardvark"); n != 0 {
		t.Fatalf("stale FTS rows survived a re-index: %d hits for the OLD content", n)
	}
	if ftsHits(t, db, "bumblebee") == 0 {
		t.Fatal("expected the new content to be FTS-searchable")
	}
	chunks2, err := db.CountChunks()
	if err != nil {
		t.Fatal(err)
	}
	if chunks2 != chunks1 {
		t.Fatalf("chunk count drifted across re-index: %d then %d (orphaned chunk rows?)", chunks1, chunks2)
	}
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
	if n := ftsHits(t, db, "remove"); n != 0 {
		t.Fatalf("deleted file's FTS rows survived: %d hits", n)
	}
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
	chunksBefore, err := db.CountChunks()
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("expected no re-chunk on rename, chunks_created=%d", stats.ChunksCreated)
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
	chunksAfter, err := db.CountChunks()
	if err != nil {
		t.Fatal(err)
	}
	if chunksAfter != chunksBefore {
		t.Fatalf("expected chunk count unchanged on rename, got %d want %d", chunksAfter, chunksBefore)
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
	if ftsHits(t, db, "remove") == 0 {
		t.Fatal("expected the document to be FTS-searchable before removal")
	}

	stats, err := ix.RemovePath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("expected 1 file removed, got %d", stats.FilesRemoved)
	}
	if n := ftsHits(t, db, "remove"); n != 0 {
		t.Fatalf("RemovePath left %d stale FTS rows", n)
	}
	if n, err := db.CountChunks(); err != nil || n != 0 {
		t.Fatalf("RemovePath left %d orphan chunk rows (err=%v)", n, err)
	}
}
