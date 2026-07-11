package index

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

// ErrEmbedderMismatch is returned by Run/RunLoop when the configured
// embedder's model doesn't match the one recorded for the existing index
// (meta key "active_embedder", written by a previous successful embed
// batch). The worker refuses to embed into a vector space built by a
// different model — the fix is a full reset, not a silent partial mix.
type ErrEmbedderMismatch struct {
	StoredModel  string
	CurrentModel string
}

func (e *ErrEmbedderMismatch) Error() string {
	return fmt.Sprintf(
		"embedder mismatch: index was built with model %q, current config uses %q\n"+
			"Run: ney reset && ney index <path>",
		e.StoredModel, e.CurrentModel,
	)
}

const (
	// defaultEmbedBatchSize mirrors the pipeline's old embed batch size.
	defaultEmbedBatchSize = 32

	// chunkIDPageSize bounds how many chunk IDs GetChunkIDsPage fetches per
	// round-trip while listing every chunk in the DB (global/unscoped mode).
	chunkIDPageSize = 2000

	// orphanBatchThreshold is the minimum orphan count worth an extra HNSW
	// full-rebuild (see vectorstore/hnsw.go: every Delete dirties the
	// graph) when pending embeds haven't fully drained yet. When pending
	// *has* fully drained, orphans are always cleaned regardless of count —
	// there's no more embedding work this cycle for the rebuild to
	// piggyback on, and skipping it would leave the self-heal race (docs
	// §4.2) unresolved until the next Notify.
	orphanBatchThreshold = 50

	// flushEveryBatches bounds how much embedded work a crash could lose
	// between flushes, similar in spirit to the pipeline's flushEveryDocs.
	flushEveryBatches = 4

	// maxConsecutiveFailures is how many retries (with backoff) a single
	// batch gets before Run gives up and returns the error — enough to ride
	// out a brief outage without hanging forever on a truly broken embedder.
	maxConsecutiveFailures = 5

	defaultBackoffBase = 30 * time.Second
	maxBackoff         = 5 * time.Minute
)

// EmbedWorker computes pending = chunk IDs - VectorStore IDs and embeds them
// in batches, entirely outside of any SQLite transaction (see
// internal/index/pipeline.go, which now only ever writes chunks/FTS, never
// vectors). It also removes orphaned vectors (VectorStore IDs with no
// corresponding chunk row) so re-chunked/deleted documents don't leak
// vectors forever.
type EmbedWorker struct {
	DB       *store.DB
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder

	// BatchSize is how many chunks are embedded per Embed call (default 32).
	BatchSize int

	// Workspace scopes embedding to one workspace by name ("" = all
	// workspaces). Orphan cleanup only ever runs in unscoped ("") mode —
	// scoped mode must never delete another workspace's valid vectors.
	Workspace string

	// MaxChunks bounds a single Run to at most this many newly embedded
	// chunks (0 = unbounded/full drain). Intended for short-lived bounded
	// syncs (e.g. `ney search`'s cwd auto-sync) that can't block on a full
	// drain of a large backlog.
	MaxChunks int

	// OnProgress, if set, is called after every successfully embedded batch
	// with cumulative (done, total) counts for this Run call.
	OnProgress func(done, total int)

	// BackoffBase overrides the initial retry backoff (tests only; defaults
	// to defaultBackoffBase). Doubles on each consecutive failure, capped at
	// maxBackoff.
	BackoffBase time.Duration

	notifyOnce sync.Once
	notifyCh   chan struct{}
}

func (w *EmbedWorker) init() {
	w.notifyOnce.Do(func() {
		w.notifyCh = make(chan struct{}, 1)
	})
}

// Notify wakes a blocked RunLoop, or — if RunLoop is mid-drain or not yet
// waiting — latches the signal in a buffered channel of size 1 so it is not
// lost: RunLoop will pick it up and immediately start another drain as soon
// as it finishes the one in progress. This is what makes the self-heal race
// in the design doc (§4.2) converge: a watcher event that lands while the
// worker is already embedding an older snapshot of "pending" still triggers
// a follow-up drain instead of going unnoticed.
func (w *EmbedWorker) Notify() {
	w.init()
	select {
	case w.notifyCh <- struct{}{}:
	default:
	}
}

func (w *EmbedWorker) batchSize() int {
	if w.BatchSize > 0 {
		return w.BatchSize
	}
	return defaultEmbedBatchSize
}

func (w *EmbedWorker) backoffBase() time.Duration {
	if w.BackoffBase > 0 {
		return w.BackoffBase
	}
	return defaultBackoffBase
}

// checkModelConsistency compares the model recorded in index_meta's
// "active_embedder" key (written by a prior successful embed batch) against
// the configured Embedder. It intentionally does NOT use
// store.DB.GetActiveEmbedder() — that method's fmt.Sscanf parse of the JSON
// blob is broken (the %s verb swallows the whole rest of the string, see
// db.go GetActiveEmbedder) — and instead re-implements the same
// substring-scan pipeline.go used to use before this check moved here.
func (w *EmbedWorker) checkModelConsistency() error {
	if w.Embedder == nil {
		return nil
	}
	raw, err := w.DB.GetMeta("active_embedder")
	if err != nil || raw == "" {
		// No prior embed recorded — fresh index (or FTS-only so far).
		// Nothing to be inconsistent with yet.
		return nil
	}
	storedModel := parseStoredModel(raw)
	if storedModel == "" {
		return nil
	}
	if storedModel != w.Embedder.ModelID() {
		return &ErrEmbedderMismatch{StoredModel: storedModel, CurrentModel: w.Embedder.ModelID()}
	}
	return nil
}

// parseStoredModel extracts the "model" field from the raw
// `{"name":"x","model":"y","dimensions":N}` blob SetActiveEmbedder writes,
// via a plain substring scan (matches pipeline.go's former
// checkEmbedderConsistency) rather than JSON decoding, to avoid pulling in
// encoding/json for one field and to stay resilient to the hand-built
// string's exact quoting.
func parseStoredModel(raw string) string {
	const marker = `"model":"`
	i := strings.Index(raw, marker)
	if i < 0 {
		return ""
	}
	rest := raw[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// Run computes pending = chunk IDs - VectorStore IDs (scoped to w.Workspace,
// or every workspace when ""), embeds it in batches, and returns once
// pending is empty (or MaxChunks chunks have been embedded, if set). It
// never holds a SQL transaction while calling Embed, and never queries the
// DB while a prior query's sql.Rows are still open (SetMaxOpenConns(1)).
//
// This is a single drain, not a daemon loop — a persistent loop belongs to
// RunLoop (used by long-lived processes like `ney mcp`, added alongside this
// for the M3 server; CLI usage just wants one drain-then-return).
func (w *EmbedWorker) Run(ctx context.Context) error {
	w.init()

	if w.Embedder == nil {
		return nil
	}
	if err := w.checkModelConsistency(); err != nil {
		return err
	}

	global := w.Workspace == ""

	allChunkIDs, err := w.listChunkIDs()
	if err != nil {
		return fmt.Errorf("list chunk ids: %w", err)
	}

	vecIDSet := make(map[string]bool, len(w.Vectors.IDs()))
	for _, id := range w.Vectors.IDs() {
		vecIDSet[id] = true
	}

	var pending []int64
	for _, id := range allChunkIDs {
		if !vecIDSet[strconv.FormatInt(id, 10)] {
			pending = append(pending, id)
		}
	}

	total := len(pending)
	if w.MaxChunks > 0 && w.MaxChunks < total {
		total = w.MaxChunks
	}

	bs := w.batchSize()
	done := 0
	batchesSinceFlush := 0
	embedderRecorded := false

	for done < total {
		if ctx.Err() != nil {
			_ = w.Vectors.Flush()
			return ctx.Err()
		}

		end := done + bs
		if end > total {
			end = total
		}
		batchIDs := pending[done:end]

		chunks, err := w.DB.GetChunksByIDs(batchIDs)
		if err != nil {
			_ = w.Vectors.Flush()
			return fmt.Errorf("load chunk batch: %w", err)
		}
		if len(chunks) == 0 {
			// Every ID in this batch was deleted concurrently (e.g. a
			// watcher re-chunked the document mid-drain). Nothing to embed
			// here; move on — the orphaned old IDs get swept in the
			// end-of-drain orphan pass below, and the new chunk IDs those
			// documents got will show up as pending on the next Run.
			done = end
			continue
		}

		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Content
		}

		vecs, err := w.embedBatch(ctx, texts)
		if err != nil {
			_ = w.Vectors.Flush()
			return err
		}

		items := make([]vectorstore.VectorItem, 0, len(chunks))
		for i, c := range chunks {
			if i < len(vecs) {
				items = append(items, vectorstore.VectorItem{
					ID:     strconv.FormatInt(c.ID, 10),
					Vector: vecs[i],
				})
			}
		}
		if err := w.Vectors.Add(ctx, items); err != nil {
			_ = w.Vectors.Flush()
			return fmt.Errorf("add vectors: %w", err)
		}

		if !embedderRecorded {
			if err := w.DB.SetActiveEmbedder(w.Embedder.ModelID(), w.Embedder.ModelID(), w.Embedder.Dimensions()); err != nil {
				_ = w.Vectors.Flush()
				return fmt.Errorf("record active embedder: %w", err)
			}
			embedderRecorded = true
		}

		done = end
		batchesSinceFlush++
		if batchesSinceFlush >= flushEveryBatches {
			batchesSinceFlush = 0
			if err := w.Vectors.Flush(); err != nil {
				return fmt.Errorf("flush vectors: %w", err)
			}
		}
		if w.OnProgress != nil {
			w.OnProgress(done, total)
		}
	}

	if err := w.Vectors.Flush(); err != nil {
		return fmt.Errorf("flush vectors: %w", err)
	}

	if global {
		remaining := len(pending) - done
		if err := w.cleanupOrphans(ctx, allChunkIDs, remaining == 0); err != nil {
			return err
		}
	}

	return nil
}

// embedBatch calls Embedder.Embed, retrying the same batch with exponential
// backoff (30s, 60s, ... capped at 5m by default) on failure. Cancellation
// is never treated as a flaky embedder: if ctx is done, or the embed error
// itself is (or wraps) a context error, embedBatch returns immediately so
// Run can flush and exit instead of sitting in a pointless backoff sleep.
// After maxConsecutiveFailures straight failures it gives up and returns the
// last error, which aborts the whole Run — at that point the embedder looks
// permanently broken, not just having a bad moment.
func (w *EmbedWorker) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	backoff := w.backoffBase()
	var lastErr error
	for attempt := 1; attempt <= maxConsecutiveFailures; attempt++ {
		vecs, err := w.Embedder.Embed(ctx, texts)
		if err == nil {
			return vecs, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		lastErr = err
		if attempt == maxConsecutiveFailures {
			break
		}
		fmt.Fprintf(os.Stderr, "warning: embed batch failed (attempt %d/%d): %v — retrying in %s\n",
			attempt, maxConsecutiveFailures, err, backoff)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil, fmt.Errorf("embedding failed %d times in a row, giving up: %w", maxConsecutiveFailures, lastErr)
}

// listChunkIDs returns every chunk ID in scope: paginated across the whole
// DB when Workspace == "", or GetChunkIDsByWorkspace otherwise. Each
// underlying query fully drains its sql.Rows (via defer Close, see
// store/db.go) before this function issues the next one — required because
// the DB connection pool is capped at 1 (CLAUDE.md).
func (w *EmbedWorker) listChunkIDs() ([]int64, error) {
	if w.Workspace == "" {
		var all []int64
		afterID := int64(0)
		for {
			page, err := w.DB.GetChunkIDsPage(afterID, chunkIDPageSize)
			if err != nil {
				return nil, err
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			afterID = page[len(page)-1]
			if len(page) < chunkIDPageSize {
				break
			}
		}
		return all, nil
	}

	ws, err := w.DB.GetWorkspaceByName(w.Workspace)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		// Unknown workspace name: nothing to embed, not an error — the
		// caller (e.g. a sync racing a workspace's first index) will see
		// the workspace appear on a later Run.
		return nil, nil
	}
	return w.DB.GetChunkIDsByWorkspace(ws.ID)
}

// cleanupOrphans removes VectorStore IDs that no longer have a chunk row
// (allChunkIDs is always the *global* chunk ID set — orphan cleanup only
// ever runs in unscoped mode, enforced by the caller). Per the design doc
// (§4.2), HNSWStore.Delete unconditionally dirties the whole graph
// regardless of how many IDs are removed, forcing a full rebuild on the
// next Search — so this batches all orphans into one Delete call, and only
// bothers when there's a real backlog (>= orphanBatchThreshold) or when
// pending has fully drained (idleDrain), matching the "batch and defer"
// rule from the spec.
func (w *EmbedWorker) cleanupOrphans(ctx context.Context, allChunkIDs []int64, idleDrain bool) error {
	chunkIDSet := make(map[string]bool, len(allChunkIDs))
	for _, id := range allChunkIDs {
		chunkIDSet[strconv.FormatInt(id, 10)] = true
	}

	var orphans []string
	for _, vid := range w.Vectors.IDs() {
		if !chunkIDSet[vid] {
			orphans = append(orphans, vid)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	if !idleDrain && len(orphans) < orphanBatchThreshold {
		return nil
	}

	if err := w.Vectors.Delete(ctx, orphans); err != nil {
		return fmt.Errorf("delete orphan vectors: %w", err)
	}
	return w.Vectors.Flush()
}

// RunLoop drains repeatedly: Run once, then block until Notify() is called
// or ctx is cancelled, then Run again. It is meant for long-lived processes
// (M3's `ney mcp` server); CLI commands should call Run directly for a
// single bounded drain. A Run error (including ErrEmbedderMismatch) is
// logged and does not stop the loop — RunLoop just waits for the next
// Notify/ctx-done, since a transient failure (or a mismatch fixed later by
// `ney reset`) shouldn't kill a background worker that's otherwise meant to
// outlive individual problems. Callers that need to react to a specific
// error (e.g. surface "blocked_mismatch" status) should call Run directly.
func (w *EmbedWorker) RunLoop(ctx context.Context) error {
	w.init()
	for {
		if err := w.Run(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			fmt.Fprintf(os.Stderr, "warning: embed worker: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.notifyCh:
		}
	}
}
