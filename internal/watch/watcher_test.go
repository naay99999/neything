package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/index"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/store"
)

func setupWatcher(t *testing.T) (w *Watcher, root string, ix *index.Indexer, db *store.DB) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "index.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	chunkResolver, err := chunk.NewResolver("character", 200, 20, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	ix = &index.Indexer{
		DB:            db,
		Loaders:       loader.NewRegistry(&loader.MarkdownLoader{}),
		ChunkResolver: chunkResolver,
		BatchSize:     8,
	}

	workspaceID, err := db.UpsertWorkspace("test", root)
	if err != nil {
		t.Fatal(err)
	}

	w = &Watcher{
		Indexer:     ix,
		RootPath:    root,
		WorkspaceID: workspaceID,
		Debounce:    50 * time.Millisecond,
		SyncEvery:   time.Hour, // keep the periodic ticker out of the way by default
	}
	return w, root, ix, db
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func countDocuments(t *testing.T, db *store.DB, workspaceID int64) int {
	t.Helper()
	docs, err := db.GetDocumentsByWorkspace(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return len(docs)
}

// TestWatcherIndexesFileWrite verifies the watcher reacts to a filesystem
// write by indexing the file through the real Indexer.
//
// It synchronizes on OnFlush (fired synchronously at the end of the flush
// closure, after stats are updated) rather than polling the DB and then
// canceling — the debounced flush runs on its own time.AfterFunc goroutine,
// independent of Run's own select loop, so canceling ctx as soon as the
// document row is visible can race a second (shutdown) flush against the
// still in-flight debounced one. Waiting for OnFlush establishes a
// happens-before edge (channel receive) between "the debounced flush
// finished" and "test calls cancel", so the two flushes never overlap.
func TestWatcherIndexesFileWrite(t *testing.T) {
	w, root, _, db := setupWatcher(t)

	flushed := make(chan struct{}, 1)
	w.OnFlush = func() {
		select {
		case flushed <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var stats *Stats
	go func() {
		defer close(done)
		s, err := w.Run(ctx)
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		stats = s
	}()

	// Give fsnotify a moment to install watches before writing.
	time.Sleep(100 * time.Millisecond)

	path := filepath.Join(root, "note.md")
	if err := os.WriteFile(path, []byte("hello world, this is a test document"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the debounced flush to run")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	if stats.FilesIndexed == 0 {
		t.Errorf("expected FilesIndexed > 0, got %+v", stats)
	}
	if countDocuments(t, db, w.WorkspaceID) == 0 {
		t.Errorf("expected the document to be persisted")
	}
}

// TestWatcherCtxCancelStopsCleanlyAndFlushes checks that canceling ctx makes
// Run return promptly, running one final flush of any pending debounced
// batch first.
func TestWatcherCtxCancelStopsCleanlyAndFlushes(t *testing.T) {
	w, root, _, db := setupWatcher(t)
	w.Debounce = 2 * time.Second // long enough that cancel races the debounce timer

	ctx, cancel := context.WithCancel(context.Background())

	resultCh := make(chan *Stats, 1)
	errCh := make(chan error, 1)
	go func() {
		s, err := w.Run(ctx)
		resultCh <- s
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)

	path := filepath.Join(root, "doc.md")
	if err := os.WriteFile(path, []byte("final flush content for the pending batch"), 0644); err != nil {
		t.Fatal(err)
	}

	// Cancel almost immediately — well before the 2s debounce timer would
	// have fired on its own — to assert ctx.Done() triggers the final flush.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after ctx cancel")
	}
	stats := <-resultCh

	if stats.FilesIndexed == 0 {
		t.Errorf("expected the final flush to have indexed the pending file, got %+v", stats)
	}
	if countDocuments(t, db, w.WorkspaceID) == 0 {
		t.Errorf("expected the document to be persisted by the final flush")
	}
}

// TestWatcherSerializeHookCalled verifies every Indexer invocation the
// watcher makes (flush + prune) routes through Serialize when set.
func TestWatcherSerializeHookCalled(t *testing.T) {
	w, root, _, _ := setupWatcher(t)
	w.SyncEvery = 100 * time.Millisecond // exercise the ticker-driven prune path too

	var calls int32
	var mu sync.Mutex
	w.Serialize = func(run func()) {
		atomic.AddInt32(&calls, 1)
		mu.Lock()
		defer mu.Unlock()
		run()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("serialize hook content"), 0644); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 5*time.Second, func() bool { return atomic.LoadInt32(&calls) >= 2 }) {
		t.Fatalf("expected Serialize to be called for both the flush and at least one prune tick, got %d calls", atomic.LoadInt32(&calls))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	// The final shutdown prune must also go through Serialize.
	if atomic.LoadInt32(&calls) < 3 {
		t.Errorf("expected Serialize to also wrap the final shutdown prune, got %d total calls", atomic.LoadInt32(&calls))
	}
}

// TestWatcherDisablePruneSkipsTicker is a structural test: with
// DisablePrune set, a very short SyncEvery must never fire a prune, and the
// final on-shutdown prune must also be skipped. We assert this by counting
// every Indexer invocation via Serialize and confirming there is exactly
// one (the single real flush) despite a ticker fast enough to have fired
// many times, and despite normal shutdown.
//
// Synchronization note: we wait on OnFlush (fired only after the flush's
// Serialize-wrapped Indexer calls have fully returned) before taking any
// snapshot of the call count or canceling ctx. The debounced flush runs on
// its own goroutine (time.AfterFunc), independent of Run's select loop, so
// canceling without this synchronization could race a concurrent shutdown
// path against a still in-flight flush.
func TestWatcherDisablePruneSkipsTicker(t *testing.T) {
	w, root, _, db := setupWatcher(t)
	w.DisablePrune = true
	w.SyncEvery = 20 * time.Millisecond // would fire many times if not disabled

	var pruneCalls int32
	w.Serialize = func(run func()) {
		atomic.AddInt32(&pruneCalls, 1)
		run()
	}
	flushed := make(chan struct{}, 1)
	w.OnFlush = func() {
		select {
		case flushed <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	path := filepath.Join(root, "b.md")
	if err := os.WriteFile(path, []byte("disable prune content"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the debounced flush to run")
	}
	if countDocuments(t, db, w.WorkspaceID) == 0 {
		t.Fatal("expected the file to have been indexed by the flush")
	}

	// Give a fast (20ms) ticker plenty of chances to fire if it weren't
	// disabled before we snapshot the call count.
	time.Sleep(200 * time.Millisecond)
	callsBeforeShutdown := atomic.LoadInt32(&pruneCalls)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	callsAfterShutdown := atomic.LoadInt32(&pruneCalls)
	if callsAfterShutdown != callsBeforeShutdown {
		t.Errorf("expected no additional Serialize calls from a final prune on shutdown when DisablePrune is set (before=%d after=%d)", callsBeforeShutdown, callsAfterShutdown)
	}
	// Exactly one Serialize call is expected total: the single flush from
	// writing b.md. No ticker prunes, no final prune.
	if callsAfterShutdown != 1 {
		t.Errorf("expected exactly 1 Serialize call (the flush only), got %d — periodic or final prune must have run despite DisablePrune", callsAfterShutdown)
	}
}

// TestAddRecursiveSkipsBuildAndDependencyDirs verifies addRecursive never
// installs an fsnotify watch on well-known build/dependency directories
// (node_modules, vendor, dist, build, target, .next, out, __pycache__,
// .venv, venv) — a real repo's node_modules alone can hold hundreds of
// thousands of files, each costing a kernel-level watch descriptor for zero
// indexing value — while still watching ordinary subdirectories.
func TestAddRecursiveSkipsBuildAndDependencyDirs(t *testing.T) {
	root := t.TempDir()

	skipped := []string{"node_modules", "vendor", "dist", "build", "target", ".next", "out", "__pycache__", ".venv", "venv"}
	for _, name := range skipped {
		dir := filepath.Join(root, name, "nested")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "file.txt"), "should never be watched")
	}

	// A normal subdirectory, and one whose name merely contains a skipped
	// name as a substring (must NOT be skipped — the match is on exact
	// basename only).
	normalDir := filepath.Join(root, "docs", "guides")
	if err := os.MkdirAll(normalDir, 0755); err != nil {
		t.Fatal(err)
	}
	notQuiteVendor := filepath.Join(root, "vendored-notes")
	if err := os.MkdirAll(notQuiteVendor, 0755); err != nil {
		t.Fatal(err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer fsw.Close()

	if err := addRecursive(fsw, root); err != nil {
		t.Fatal(err)
	}

	watched := make(map[string]bool)
	for _, p := range fsw.WatchList() {
		watched[p] = true
	}

	if !watched[root] {
		t.Errorf("expected root %s to be watched", root)
	}
	if !watched[normalDir] {
		t.Errorf("expected ordinary subdirectory %s to be watched", normalDir)
	}
	if !watched[notQuiteVendor] {
		t.Errorf("expected %s to be watched (name only *contains* a skipped name, doesn't match it exactly)", notQuiteVendor)
	}
	for _, name := range skipped {
		dir := filepath.Join(root, name)
		if watched[dir] {
			t.Errorf("expected %s to NOT be watched (build/dependency dir)", dir)
		}
		if watched[filepath.Join(dir, "nested")] {
			t.Errorf("expected nested dir under %s to NOT be watched either (never descended into)", dir)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestWatcherShutdownDoesNotRaceFlush drives the exact window the flush lock
// exists for. time.Timer.Stop does not wait for a callback that has already
// started, so a debounce flush can still be running when Run performs its
// final flush and reads Stats — putting two Indexer invocations in flight at
// once (Serialize is nil here, as in plain `ney watch`) and mutating Stats
// concurrently with Run's return.
//
// The overlap is forced rather than raced for: OnEvent, which the flush body
// calls per indexed file, parks the first flush until the test has cancelled
// the context. Run's shutdown then necessarily coincides with it. Meaningful
// under -race, and verified to fail without the fix.
func TestWatcherShutdownDoesNotRaceFlush(t *testing.T) {
	w, root, _, db := setupWatcher(t)
	defer db.Close()

	w.Debounce = 5 * time.Millisecond

	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	w.OnEvent = func(string) {
		once.Do(func() {
			close(entered)
			<-gate
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *Stats, 1)
	go func() {
		s, err := w.Run(ctx)
		if err != nil {
			t.Errorf("run: %v", err)
		}
		done <- s
	}()

	// One write is enough: it schedules a debounce flush, which parks in
	// OnEvent below.
	if !waitFor(t, 5*time.Second, func() bool {
		_ = os.WriteFile(filepath.Join(root, "n.md"), []byte("content"), 0o644)
		select {
		case <-entered:
			return true
		default:
			return false
		}
	}) {
		close(gate)
		cancel()
		<-done
		t.Fatal("the debounce flush never started")
	}

	// A flush is now parked mid-body. Shut down on top of it, then release it
	// so the two run concurrently.
	cancel()
	close(gate)

	select {
	case stats := <-done:
		if stats == nil {
			t.Fatal("expected stats from a cancelled watcher")
		}
		// Reading every field: the race detector reports an unsynchronized
		// read here if the parked flush is still writing.
		t.Logf("indexed=%d removed=%d errors=%d", stats.FilesIndexed, stats.FilesRemoved, stats.Errors)
	case <-time.After(30 * time.Second):
		t.Fatal("watcher did not shut down")
	}
}
