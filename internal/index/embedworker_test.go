package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/naay99999/neything/internal/vectorstore"
)

// runEmbedWorker drains an Indexer's pending embeds fully using its own
// DB/Vectors/Embedder. It's the standard "Phase B" step tests reach for
// after ix.Index (Phase A) when they need actual vectors to exist — since
// the pipeline no longer embeds anything itself.
func runEmbedWorker(t *testing.T, ix *Indexer) {
	t.Helper()
	w := &EmbedWorker{DB: ix.DB, Vectors: ix.Vectors, Embedder: ix.Embedder}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("embed worker run: %v", err)
	}
}

// flakyEmbedder fails the first failsLeft calls, then delegates to a real
// (mock) embedder.
type flakyEmbedder struct {
	mock      *mockEmbedder
	failsLeft int
	calls     int
}

func (f *flakyEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls++
	if f.failsLeft > 0 {
		f.failsLeft--
		return nil, fmt.Errorf("simulated transient embed failure")
	}
	return f.mock.Embed(ctx, texts)
}
func (f *flakyEmbedder) Dimensions() int { return f.mock.Dimensions() }
func (f *flakyEmbedder) ModelID() string { return f.mock.ModelID() }

// alwaysFailEmbedder never succeeds — used to exercise the "gives up after
// maxConsecutiveFailures" path.
type alwaysFailEmbedder struct{ calls int }

func (a *alwaysFailEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	a.calls++
	return nil, fmt.Errorf("permanent embed failure")
}
func (a *alwaysFailEmbedder) Dimensions() int { return testDim }
func (a *alwaysFailEmbedder) ModelID() string { return "always-fail" }

// slowEmbedder sleeps delay (respecting ctx) before delegating — used to
// give tests a window to interleave concurrent work while a batch is being
// embedded.
type slowEmbedder struct {
	mock  *mockEmbedder
	delay time.Duration
}

func (s *slowEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.mock.Embed(ctx, texts)
}
func (s *slowEmbedder) Dimensions() int { return s.mock.Dimensions() }
func (s *slowEmbedder) ModelID() string { return s.mock.ModelID() }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// --- Phase A only, both nil and non-nil embedder ---------------------------

func TestPhaseAOnlyStatsNonNilEmbedder(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document about invoices and billing")

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}
	if stats.ChunksPendingEmbed != stats.ChunksCreated {
		t.Fatalf("ChunksPendingEmbed=%d want %d (== ChunksCreated, nothing embedded in Phase A)",
			stats.ChunksPendingEmbed, stats.ChunksCreated)
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected 0 vectors after Phase A even with an embedder configured, got %d", got)
	}
	chunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(chunkIDs) != stats.ChunksCreated {
		t.Fatalf("chunk rows=%d want %d", len(chunkIDs), stats.ChunksCreated)
	}
	res, err := db.SearchFTS("invoices", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected FTS rows written in Phase A")
	}
}

func TestPhaseAOnlyStatsNilEmbedder(t *testing.T) {
	ix, db, dir := setupIndexerNilEmbedder(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document about invoices and billing")

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}
	if stats.ChunksPendingEmbed != stats.ChunksCreated {
		t.Fatalf("ChunksPendingEmbed=%d want %d", stats.ChunksPendingEmbed, stats.ChunksCreated)
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected 0 vectors, got %d", got)
	}
	_ = db
}

// --- Worker drains pending fully --------------------------------------------

func TestEmbedWorkerDrainsPendingFully(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document about invoices")
	writeFile(t, filepath.Join(root, "b.md"), "beta document about widgets and gadgets galore")

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}

	w := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: ix.Embedder}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := ix.Vectors.Count(); got != stats.ChunksCreated {
		t.Fatalf("expected %d vectors after drain, got %d", stats.ChunksCreated, got)
	}

	raw, err := db.GetMeta("active_embedder")
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("expected active_embedder meta to be written after first successful batch")
	}
	if got := parseStoredModel(raw); got != ix.Embedder.ModelID() {
		t.Fatalf("stored model=%q want %q", got, ix.Embedder.ModelID())
	}
}

// --- Scoped worker -----------------------------------------------------------

func TestEmbedWorkerScopedToWorkspace(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	rootA := filepath.Join(dir, "a")
	rootB := filepath.Join(dir, "b")
	writeFile(t, filepath.Join(rootA, "a.md"), "workspace A document about invoices")
	writeFile(t, filepath.Join(rootB, "b.md"), "workspace B document about widgets")

	if _, err := ix.Index(context.Background(), rootA, "wsA"); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Index(context.Background(), rootB, "wsB"); err != nil {
		t.Fatal(err)
	}

	wsA, err := db.GetWorkspaceByName("wsA")
	if err != nil || wsA == nil {
		t.Fatal("expected wsA to exist")
	}
	wsB, err := db.GetWorkspaceByName("wsB")
	if err != nil || wsB == nil {
		t.Fatal("expected wsB to exist")
	}
	chunksA, err := db.GetChunkIDsByWorkspace(wsA.ID)
	if err != nil || len(chunksA) == 0 {
		t.Fatal("expected chunks in wsA")
	}
	chunksB, err := db.GetChunkIDsByWorkspace(wsB.ID)
	if err != nil || len(chunksB) == 0 {
		t.Fatal("expected chunks in wsB")
	}

	// Seed a vector for wsB that has no matching chunk, to prove scoped
	// mode never runs orphan cleanup (which would delete it even though
	// it doesn't belong to wsA).
	bogusID := "999999999"
	if err := ix.Vectors.Add(context.Background(), []vectorstore.VectorItem{
		{ID: bogusID, Vector: hashToVector("bogus", testDim)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Vectors.Flush(); err != nil {
		t.Fatal(err)
	}

	w := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: ix.Embedder, Workspace: "wsA"}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	vecIDs := make(map[string]bool)
	for _, id := range ix.Vectors.IDs() {
		vecIDs[id] = true
	}
	for _, id := range chunksA {
		if !vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("expected wsA chunk %d embedded", id)
		}
	}
	for _, id := range chunksB {
		if vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("wsB chunk %d embedded by a wsA-scoped worker", id)
		}
	}
	if !vecIDs[bogusID] {
		t.Fatal("scoped worker must not run orphan cleanup — bogus vector should survive")
	}
}

// --- Orphan cleanup ----------------------------------------------------------

func TestEmbedWorkerOrphanCleanupGlobalDrain(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document about invoices")

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	orphanID := "424242424"
	if err := ix.Vectors.Add(context.Background(), []vectorstore.VectorItem{
		{ID: orphanID, Vector: hashToVector("orphan", testDim)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Vectors.Flush(); err != nil {
		t.Fatal(err)
	}

	w := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: ix.Embedder}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, id := range ix.Vectors.IDs() {
		if id == orphanID {
			t.Fatal("expected orphan vector to be removed after global drain")
		}
	}
	chunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Vectors.Count(); got != len(chunkIDs) {
		t.Fatalf("vector count=%d want %d (== real chunk count, orphan gone)", got, len(chunkIDs))
	}
}

// --- Simulated race: re-chunk mid-drain, converges after a second Run ------

func TestEmbedWorkerRaceConvergesAfterSecondRun(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	pathA := filepath.Join(root, "a.md")
	pathB := filepath.Join(root, "b.md")
	writeFile(t, pathA, "alpha document content")
	writeFile(t, pathB, "beta document content")

	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	docB, err := db.GetDocumentByPath(pathB)
	if err != nil || docB == nil {
		t.Fatal("expected doc B")
	}
	oldChunksB, err := db.GetChunkIDsByDocument(docB.ID)
	if err != nil || len(oldChunksB) == 0 {
		t.Fatal("expected doc B chunks")
	}

	// Race: the first time Embed is called (processing doc A's batch,
	// since chunk IDs are assigned in creation order and BatchSize=1 makes
	// doc A's chunk batch #1), rewrite doc B's content — simulating a
	// watcher re-chunking it concurrently. Run() already snapshotted the
	// old pending list, so doc B's *new* chunk ID is invisible to this
	// Run — and its old chunk ID, now deleted from `chunks`, becomes a
	// vector-store orphan once embedded (Run embeds whatever content
	// GetChunksByIDs returned, and it was fetched before the race — but
	// once the doc B row changes chunk IDs, the pre-race ID is gone from
	// `chunks`, so this run's own orphan-pass snapshot still call it
	// "valid" — it only gets swept on a second Run once listChunkIDs()
	// picks up the new reality).
	race := &raceOnceEmbedder{
		mock: &mockEmbedder{},
		trigger: func() {
			writeFile(t, pathB, "beta document content REWRITTEN mid-drain")
			if _, err := ix.Index(context.Background(), root, "test"); err != nil {
				t.Fatal(err)
			}
		},
	}

	w := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: race, BatchSize: 1}
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// New doc B content now indexed under new chunk IDs.
	docB2, err := db.GetDocumentByPath(pathB)
	if err != nil || docB2 == nil {
		t.Fatal("expected doc B still present after rewrite")
	}
	newChunksB, err := db.GetChunkIDsByDocument(docB2.ID)
	if err != nil || len(newChunksB) == 0 {
		t.Fatal("expected doc B chunks after rewrite")
	}

	// Second Run converges: new doc B chunks get embedded, and any stale
	// vector left over from the old (now-deleted) doc B chunk IDs gets
	// swept by the orphan pass.
	w2 := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: &mockEmbedder{}}
	if err := w2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	vecIDs := make(map[string]bool)
	for _, id := range ix.Vectors.IDs() {
		vecIDs[id] = true
	}
	for _, id := range newChunksB {
		if !vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("expected new doc B chunk %d embedded after convergence", id)
		}
	}
	for _, id := range oldChunksB {
		if vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("expected stale old doc B chunk %d vector removed after convergence", id)
		}
	}

	allChunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Vectors.Count(); got != len(allChunkIDs) {
		t.Fatalf("final vector count=%d want %d (full convergence)", got, len(allChunkIDs))
	}
}

type raceOnceEmbedder struct {
	mock    *mockEmbedder
	trigger func()
	fired   bool
}

func (r *raceOnceEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !r.fired {
		r.fired = true
		if r.trigger != nil {
			r.trigger()
		}
	}
	return r.mock.Embed(ctx, texts)
}
func (r *raceOnceEmbedder) Dimensions() int { return r.mock.Dimensions() }
func (r *raceOnceEmbedder) ModelID() string { return r.mock.ModelID() }

// --- Model mismatch ----------------------------------------------------------

func TestEmbedWorkerModelMismatch(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document content")
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	if err := db.SetActiveEmbedder("other-provider", "other-model", 32); err != nil {
		t.Fatal(err)
	}

	w := &EmbedWorker{DB: db, Vectors: ix.Vectors, Embedder: ix.Embedder}
	err := w.Run(context.Background())
	if err == nil {
		t.Fatal("expected ErrEmbedderMismatch")
	}
	var mismatch *ErrEmbedderMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *ErrEmbedderMismatch, got %T: %v", err, err)
	}
	if mismatch.StoredModel != "other-model" || mismatch.CurrentModel != ix.Embedder.ModelID() {
		t.Fatalf("unexpected mismatch fields: %+v", mismatch)
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected no embedding to happen on mismatch, got %d vectors", got)
	}
}

// --- Backoff -----------------------------------------------------------------

func TestEmbedWorkerBackoffRetriesThenSucceeds(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document content")
	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}

	flaky := &flakyEmbedder{mock: &mockEmbedder{}, failsLeft: 2}
	w := &EmbedWorker{
		DB:          db,
		Vectors:     ix.Vectors,
		Embedder:    flaky,
		BackoffBase: time.Millisecond, // keep the test fast
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("expected Run to recover after retries, got: %v", err)
	}
	if flaky.calls < 3 {
		t.Fatalf("expected at least 3 embed attempts (2 failures + 1 success), got %d", flaky.calls)
	}
	if got := ix.Vectors.Count(); got != stats.ChunksCreated {
		t.Fatalf("expected %d vectors after recovery, got %d", stats.ChunksCreated, got)
	}
}

// TestEmbedWorkerPermanentFailureQuarantines exercises the single-chunk leaf
// of embedChunksBisect's poison-isolation logic (fix: "poison batch aborts
// the whole drain" — bisection should isolate a non-systemic failure and let
// the drain finish cleanly instead of aborting the whole Run). BatchSize: 1
// forces every batch Run hands to embedChunksBisect down to a single chunk
// from the start, so this exercises the len(chunks)==1 base case directly
// without going through the recursive split (that's
// TestEmbedWorkerBisectionIsolatesPoisonChunk).
func TestEmbedWorkerPermanentFailureQuarantines(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document content")
	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}

	fail := &alwaysFailEmbedder{}
	w := &EmbedWorker{
		DB:          db,
		Vectors:     ix.Vectors,
		Embedder:    fail,
		BatchSize:   1,
		BackoffBase: time.Millisecond,
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("expected Run to quarantine every poison chunk and finish cleanly, got: %v", err)
	}
	wantCalls := maxConsecutiveFailures * stats.ChunksCreated
	if fail.calls != wantCalls {
		t.Fatalf("expected exactly %d attempts (maxConsecutiveFailures * %d chunks) before quarantining, got %d",
			wantCalls, stats.ChunksCreated, fail.calls)
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected no vectors added for the quarantined chunks, got %d", got)
	}

	// A second Run on the same worker must not retry any quarantined chunk
	// (whether because they're still in w.quarantined, or because the
	// short-circuit watermark from the first drain proves nothing changed —
	// either way, no further embed calls).
	callsBefore := fail.calls
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if fail.calls != callsBefore {
		t.Fatalf("expected quarantined chunks not to be retried on a second Run, got %d more calls", fail.calls-callsBefore)
	}
}

// authFailEmbedder always fails with an auth-shaped error message, used to
// verify Run aborts (rather than bisecting/quarantining) on what looks like
// a systemic failure.
type authFailEmbedder struct{ calls int }

func (a *authFailEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	a.calls++
	return nil, fmt.Errorf("401 Unauthorized: invalid api key")
}
func (a *authFailEmbedder) Dimensions() int { return testDim }
func (a *authFailEmbedder) ModelID() string { return "auth-fail" }

func TestEmbedWorkerSystemicErrorAbortsRun(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document content")
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	fail := &authFailEmbedder{}
	w := &EmbedWorker{
		DB:          db,
		Vectors:     ix.Vectors,
		Embedder:    fail,
		BackoffBase: time.Millisecond,
	}
	err := w.Run(context.Background())
	if err == nil {
		t.Fatal("expected Run to abort on a systemic (auth) error rather than quarantine")
	}
	if got := ix.Vectors.Count(); got != 0 {
		t.Fatalf("expected no vectors added, got %d", got)
	}
}

// poisonEmbedder fails any batch containing a text with the "bad" marker
// substring, succeeding (via the wrapped mock) on any batch without it —
// simulating a single chunk whose content the embedding API can never
// process, while its batch-mates embed fine.
type poisonEmbedder struct {
	mock  *mockEmbedder
	bad   string
	calls int
}

func (p *poisonEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.calls++
	for _, t := range texts {
		if strings.Contains(t, p.bad) {
			return nil, fmt.Errorf("simulated poison chunk failure")
		}
	}
	return p.mock.Embed(ctx, texts)
}
func (p *poisonEmbedder) Dimensions() int { return p.mock.Dimensions() }
func (p *poisonEmbedder) ModelID() string { return p.mock.ModelID() }

// TestEmbedWorkerBisectionIsolatesPoisonChunk is the core bisection test:
// whichever chunk(s) actually contain the "POISON" marker text always fail
// to embed (poisonEmbedder fails a whole batch if any text in it contains
// the marker); every other chunk — including any non-marker chunk the
// chunker may have split poison.md's own content into — must still get
// embedded. Forcing BatchSize to cover every chunk in one call means
// embedChunksBisect has to actually split the batch to find the poison
// chunk(s) — a batch of 1 wouldn't exercise the recursive case at all
// (that's TestEmbedWorkerPermanentFailureQuarantines).
func TestEmbedWorkerBisectionIsolatesPoisonChunk(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha short doc")
	writeFile(t, filepath.Join(root, "b.md"), "beta short doc")
	writeFile(t, filepath.Join(root, "poison.md"), "POISON marker doc that always fails to embed")
	writeFile(t, filepath.Join(root, "c.md"), "gamma short doc")
	writeFile(t, filepath.Join(root, "d.md"), "delta short doc")

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.ChunksCreated < 2 {
		t.Fatalf("expected at least 2 chunks to exercise bisection, got %d", stats.ChunksCreated)
	}

	allChunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	allChunks, err := db.GetChunksByIDs(allChunkIDs)
	if err != nil {
		t.Fatal(err)
	}
	var poisonIDs, okIDs []int64
	for _, c := range allChunks {
		if strings.Contains(c.Content, "POISON") {
			poisonIDs = append(poisonIDs, c.ID)
		} else {
			okIDs = append(okIDs, c.ID)
		}
	}
	if len(poisonIDs) == 0 {
		t.Fatal("expected at least one chunk to contain the POISON marker")
	}

	poison := &poisonEmbedder{mock: &mockEmbedder{}, bad: "POISON"}
	w := &EmbedWorker{
		DB:          db,
		Vectors:     ix.Vectors,
		Embedder:    poison,
		BatchSize:   stats.ChunksCreated, // force every chunk into one batch
		BackoffBase: time.Millisecond,
		BisectDelay: time.Millisecond,
	}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("expected Run to isolate the poison chunk(s) and finish cleanly, got: %v", err)
	}

	if got := ix.Vectors.Count(); got != len(okIDs) {
		t.Fatalf("expected %d vectors (every non-poison chunk), got %d", len(okIDs), got)
	}

	vecIDs := make(map[string]bool)
	for _, id := range ix.Vectors.IDs() {
		vecIDs[id] = true
	}
	for _, id := range poisonIDs {
		if vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("expected poison chunk %d to be quarantined, not embedded", id)
		}
	}
	for _, id := range okIDs {
		if !vecIDs[strconv.FormatInt(id, 10)] {
			t.Fatalf("expected non-poison chunk %d to be embedded", id)
		}
	}
}

// --- Batch-size provider capability ------------------------------------------

// capEmbedder implements the batchSizer capability EmbedWorker.batchSize()
// looks for.
type capEmbedder struct {
	mock *mockEmbedder
	max  int
}

func (c *capEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return c.mock.Embed(ctx, texts)
}
func (c *capEmbedder) Dimensions() int   { return c.mock.Dimensions() }
func (c *capEmbedder) ModelID() string   { return c.mock.ModelID() }
func (c *capEmbedder) MaxBatchSize() int { return c.max }

func TestEmbedWorkerBatchSizeUsesProviderCapability(t *testing.T) {
	if got := (&EmbedWorker{Embedder: &capEmbedder{mock: &mockEmbedder{}, max: 100}}).batchSize(); got != 100 {
		t.Fatalf("expected provider capability (100) to override the default, got %d", got)
	}

	if got := (&EmbedWorker{
		Embedder:  &capEmbedder{mock: &mockEmbedder{}, max: 100},
		BatchSize: 10,
	}).batchSize(); got != 10 {
		t.Fatalf("expected explicit smaller BatchSize (10) to win over provider max, got %d", got)
	}

	if got := (&EmbedWorker{
		Embedder:  &capEmbedder{mock: &mockEmbedder{}, max: 100},
		BatchSize: 500,
	}).batchSize(); got != 100 {
		t.Fatalf("expected min(explicit, providerMax) = 100, got %d", got)
	}

	if got := (&EmbedWorker{Embedder: &mockEmbedder{}}).batchSize(); got != defaultEmbedBatchSize {
		t.Fatalf("expected default batch size %d for an embedder without the capability, got %d", defaultEmbedBatchSize, got)
	}
}

// --- Notify latching ---------------------------------------------------------

func TestEmbedWorkerNotifyLatchedWhileBusy(t *testing.T) {
	w := &EmbedWorker{}
	w.init()

	// A signal sent before anyone is listening must still be observable —
	// this is the buffered-channel latch the design doc calls out as
	// required for the self-heal race to converge.
	w.Notify()
	select {
	case <-w.notifyCh:
	default:
		t.Fatal("expected latched notify signal to be available")
	}

	// Repeated notifies beyond the buffer's capacity must not block.
	w.Notify()
	w.Notify()
	select {
	case <-w.notifyCh:
	default:
		t.Fatal("expected a notify signal to still be available")
	}
}

func TestEmbedWorkerRunLoopNotifyLatching(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	pathA := filepath.Join(root, "a.md")
	writeFile(t, pathA, "alpha document content")
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}

	// Default BatchSize (32) so each doc's chunks drain in a single slow
	// batch: the 60ms sleep is the window in which the test injects doc B
	// and notifies. (A tiny BatchSize would pay that sleep per chunk — and
	// short docs still yield ~20 overlapping chunks with this chunker — so
	// the drain would outlive the ctx before doc B's turn ever came.)
	slow := &slowEmbedder{mock: &mockEmbedder{}, delay: 60 * time.Millisecond}
	w := &EmbedWorker{
		DB:       db,
		Vectors:  ix.Vectors,
		Embedder: slow,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- w.RunLoop(ctx) }()

	// Let the first Run start (and start sleeping inside Embed for doc A).
	time.Sleep(20 * time.Millisecond)

	// Doc B's chunk is invisible to the pending snapshot the in-flight
	// first Run already captured. Notify while that Run is still busy: if
	// the signal were lost, RunLoop would go straight to blocking on
	// ctx.Done() after the first Run finishes, and doc B would never get
	// embedded within this ctx window.
	pathB := filepath.Join(root, "b.md")
	writeFile(t, pathB, "beta document content")
	if _, err := ix.Index(context.Background(), root, "test"); err != nil {
		t.Fatal(err)
	}
	w.Notify()

	docB, err := db.GetDocumentByPath(pathB)
	if err != nil || docB == nil {
		t.Fatal("expected doc B indexed")
	}
	chunkIDs, err := db.GetChunkIDsByDocument(docB.ID)
	if err != nil || len(chunkIDs) == 0 {
		t.Fatal("expected doc B chunks")
	}

	// Poll until the latched notify's follow-up drain embeds doc B, then
	// shut the loop down. If the signal were lost, RunLoop would sit
	// blocked after the first drain, doc B would never converge, and this
	// times out at the ctx deadline.
	deadline := time.After(2500 * time.Millisecond)
	for {
		vecIDs := make(map[string]bool)
		for _, id := range ix.Vectors.IDs() {
			vecIDs[id] = true
		}
		converged := true
		for _, id := range chunkIDs {
			if !vecIDs[strconv.FormatInt(id, 10)] {
				converged = false
				break
			}
		}
		if converged {
			break
		}
		select {
		case <-deadline:
			t.Fatal("doc B chunks not embedded — notify signal appears to have been lost")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected RunLoop to return context.Canceled, got %v", err)
	}
}

// --- End-state equivalence: Phase A + worker vs. the old fused flow --------

func TestEndStateEquivalencePhaseAPlusWorker(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	writeFile(t, filepath.Join(root, "a.md"), "alpha document about invoices")
	writeFile(t, filepath.Join(root, "b.md"), "beta document about widgets and gadgets galore")
	writeFile(t, filepath.Join(root, "c.md"), "gamma notes on quarterly revenue trends")

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ix.Vectors.Count() != 0 {
		t.Fatalf("expected 0 vectors after Phase A, got %d", ix.Vectors.Count())
	}
	if stats.ChunksPendingEmbed != stats.ChunksCreated {
		t.Fatalf("ChunksPendingEmbed=%d want %d", stats.ChunksPendingEmbed, stats.ChunksCreated)
	}

	runEmbedWorker(t, ix)

	chunkIDs, err := db.GetAllChunkIDs()
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := make(map[string]bool, len(chunkIDs))
	for _, id := range chunkIDs {
		wantIDs[strconv.FormatInt(id, 10)] = true
	}
	gotIDs := ix.Vectors.IDs()
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("vector count %d != chunk count %d", len(gotIDs), len(wantIDs))
	}
	for _, id := range gotIDs {
		if !wantIDs[id] {
			t.Fatalf("unexpected vector id %s with no matching chunk", id)
		}
	}

	res, err := db.SearchFTS("invoices", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("expected FTS match for invoices")
	}
}
