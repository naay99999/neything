package main

import (
	"fmt"
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

// TestServerStateDiscoveredCapEvictsOldest: a long-running `ney mcp` process
// must not let serverState.discovered (the read_document allowlist that
// search_folder populates) grow without bound over its lifetime — once
// discoveredCap entries are recorded, adding one more must evict the
// oldest rather than keep growing.
func TestServerStateDiscoveredCapEvictsOldest(t *testing.T) {
	s := newServerState(nil, false)

	// Fill exactly to the cap.
	for i := 0; i < discoveredCap; i++ {
		s.addDiscovered(fmt.Sprintf("/path/%d", i))
	}
	if !s.isDiscovered("/path/0") {
		t.Fatal("expected the first-added path to still be discovered before the cap is exceeded")
	}
	if len(s.discoveredOrder) != discoveredCap {
		t.Fatalf("expected discoveredOrder to hold exactly %d entries, got %d", discoveredCap, len(s.discoveredOrder))
	}

	// One more entry should evict the oldest (path 0), not grow past the cap.
	s.addDiscovered("/path/overflow")
	if s.isDiscovered("/path/0") {
		t.Fatal("expected the oldest discovered path to be evicted once the cap was exceeded")
	}
	if !s.isDiscovered("/path/1") {
		t.Fatal("expected the second-oldest path to survive eviction")
	}
	if !s.isDiscovered("/path/overflow") {
		t.Fatal("expected the newly discovered path to be present")
	}
	if len(s.discoveredOrder) != discoveredCap {
		t.Fatalf("expected discoveredOrder to stay capped at %d entries, got %d", discoveredCap, len(s.discoveredOrder))
	}

	// Re-discovering an already-known path must not grow the set or evict
	// anything (it's a no-op once present).
	before := len(s.discoveredOrder)
	s.addDiscovered("/path/overflow")
	if len(s.discoveredOrder) != before {
		t.Fatalf("re-adding an already-discovered path should be a no-op, size changed %d -> %d", before, len(s.discoveredOrder))
	}
}
