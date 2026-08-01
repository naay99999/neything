package index

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/pathfilter"
)

// TestIndexSkipsExcludedFiles: the walk must never index dotfiles or
// secret-named files, even when their extension is supported.
func TestIndexSkipsExcludedFiles(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeExcludeTestFile(t, filepath.Join(root, "notes.md"), "# Notes\n\nnormal content here")
	writeExcludeTestFile(t, filepath.Join(root, ".hidden.md"), "hidden dotfile content")
	writeExcludeTestFile(t, filepath.Join(root, "secrets.md"), "database password: hunter2")
	writeExcludeTestFile(t, filepath.Join(root, "credentials.json"), `{"token":"abc"}`)
	writeExcludeTestFile(t, filepath.Join(root, ".ssh-backup", "config.md"), "inside a hidden dir")

	stats, err := ix.Index(context.Background(), root, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 {
		t.Fatalf("expected exactly 1 file scanned (notes.md), got %d", stats.FilesScanned)
	}

	ws, err := db.GetWorkspaceByName("ws")
	if err != nil || ws == nil {
		t.Fatalf("workspace missing: %v", err)
	}
	docs, err := db.GetDocumentsByWorkspace(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || filepath.Base(docs[0].Path) != "notes.md" {
		t.Fatalf("expected only notes.md indexed, got %+v", docs)
	}
}

// TestIndexPrunesNewlyExcludedFiles: a file indexed before it matched a deny
// pattern must be pruned on the next run once the pattern applies (self-heal
// for already-leaked secrets).
func TestIndexPrunesNewlyExcludedFiles(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeExcludeTestFile(t, filepath.Join(root, "notes.md"), "normal content")
	writeExcludeTestFile(t, filepath.Join(root, "plans.md"), "quarterly plans")

	if _, err := ix.Index(context.Background(), root, "ws"); err != nil {
		t.Fatal(err)
	}

	// A stricter user config lands: plans.md is excluded going forward.
	flt, err := pathfilter.New([]string{"plans.md"})
	if err != nil {
		t.Fatal(err)
	}
	ix.Filter = flt

	stats, err := ix.Index(context.Background(), root, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesRemoved != 1 {
		t.Fatalf("expected the newly-excluded plans.md to be pruned, got FilesRemoved=%d", stats.FilesRemoved)
	}

	ws, _ := db.GetWorkspaceByName("ws")
	docs, err := db.GetDocumentsByWorkspace(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || filepath.Base(docs[0].Path) != "notes.md" {
		t.Fatalf("expected only notes.md to remain, got %+v", docs)
	}
}

// TestIndexPathExcludedIsSilentSkip: the watcher hands IndexPath every saved
// file; an excluded one must be a skip, not an error.
func TestIndexPathExcludedIsSilentSkip(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	secret := filepath.Join(root, "passwords.md")
	writeExcludeTestFile(t, secret, "top secret")

	wsID, err := db.UpsertWorkspace("ws", root)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ix.IndexPath(context.Background(), secret, wsID, "ws")
	if err != nil {
		t.Fatalf("excluded file should be a silent skip, got error: %v", err)
	}
	if stats.FilesSkipped != 1 || stats.ChunksCreated != 0 {
		t.Fatalf("expected skip with no chunks, got %+v", stats)
	}
}

func writeExcludeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestIndexSkipsSymlinks is the regression guard for a confirmed content
// leak: the walk checked only the entry's *name*, and os.ReadFile then
// followed the link. A symlink called readme.md pointing at a private key got
// the key's contents chunked into the index and returned as a search snippet.
func TestIndexSkipsSymlinks(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	secret := filepath.Join(dir, "id_rsa")
	writeExcludeTestFile(t, secret, "BEGIN PRIVATE KEY SUPERSECRETMATERIAL")

	root := filepath.Join(dir, "notes")
	writeExcludeTestFile(t, filepath.Join(root, "real.md"), "ordinary note content")
	if err := os.Symlink(secret, filepath.Join(root, "readme.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	stats, err := ix.Index(context.Background(), root, "ws")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 {
		t.Fatalf("expected only the regular file to be scanned, got %d", stats.FilesScanned)
	}

	hits, err := db.SearchFTS("SUPERSECRETMATERIAL", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("symlink target was indexed — %d FTS hit(s) for the secret", len(hits))
	}
}

// TestIndexPathSkipsNonRegularFiles covers the watcher's entry point, which
// has no DirEntry and so needs its own Lstat-based check.
func TestIndexPathSkipsNonRegularFiles(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	secret := filepath.Join(dir, "id_rsa")
	writeExcludeTestFile(t, secret, "BEGIN PRIVATE KEY SUPERSECRETMATERIAL")

	root := filepath.Join(dir, "notes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "readme.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	wsID, err := db.UpsertWorkspace("ws", root)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ix.IndexPath(context.Background(), link, wsID, "ws")
	if err != nil {
		t.Fatalf("a symlink should be a silent skip, got error: %v", err)
	}
	if stats.FilesSkipped != 1 || stats.ChunksCreated != 0 {
		t.Fatalf("expected skip with no chunks, got %+v", stats)
	}
	hits, err := db.SearchFTS("SUPERSECRETMATERIAL", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("symlink target was indexed via IndexPath — %d FTS hit(s)", len(hits))
	}
}

// TestIndexDoesNotHangOnNamedPipe: a FIFO with an indexable extension used to
// reach os.ReadFile, which blocks until a writer appears — wedging the whole
// indexing pass. The test's own timeout is the assertion.
func TestIndexDoesNotHangOnNamedPipe(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "notes")
	writeExcludeTestFile(t, filepath.Join(root, "real.md"), "ordinary note content")
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.md"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := ix.Index(context.Background(), root, "ws"); err != nil {
			t.Errorf("index: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("indexing blocked on a named pipe")
	}
}
