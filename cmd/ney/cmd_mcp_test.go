package main

import (
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/store"
)

// TestAcquireWriterLockFallsBackToReadOnly: a second acquire against the
// same dir must not fail — it degrades to read-only so a second MCP client
// still gets served.
func TestAcquireWriterLockFallsBackToReadOnly(t *testing.T) {
	dir := t.TempDir()

	first, readOnly, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || readOnly {
		t.Fatal("first acquire should get the lock read-write")
	}
	defer first.Release()

	second, readOnly, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("second acquire should degrade, not error: %v", err)
	}
	if second != nil || !readOnly {
		t.Fatalf("second acquire should be read-only with a nil lock, got lock=%v readOnly=%v", second, readOnly)
	}
	// Release on the nil lock must be safe — runMCP defers it unconditionally.
	second.Release()

	// After the holder releases, acquire works read-write again.
	first.Release()
	third, readOnly, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if third == nil || readOnly {
		t.Fatal("acquire after release should get the lock read-write again")
	}
	third.Release()
}

// TestResolveMCPRootsReadOnlyDoesNotPersist: read-only mode must not write a
// workspace row for --root paths.
func TestResolveMCPRootsReadOnlyDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	roots, err := resolveMCPRoots(db, []string{root}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].Path != resolvedRoot {
		t.Fatalf("expected the root to be served un-persisted, got %+v", roots)
	}

	wss, err := db.ListWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(wss) != 0 {
		t.Fatalf("read-only resolve must not write workspace rows, got %+v", wss)
	}

	// Same call in read-write mode does persist.
	if _, err := resolveMCPRoots(db, []string{root}, false); err != nil {
		t.Fatal(err)
	}
	wss, err = db.ListWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(wss) != 1 {
		t.Fatalf("read-write resolve should persist the workspace, got %+v", wss)
	}
}
