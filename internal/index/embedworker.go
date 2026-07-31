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
	// Only used when the configured Embedder doesn't report a preferred
	// batch size via the batchSizer capability (see batchSize()).
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

	// flushInterval bounds how much embedded work a crash could lose between
	// flushes. It replaces a batch-count trigger (flush every N batches):
	// with provider-aware batch sizes now up to ~100 chunks/batch (see
	// batchSize()), a fixed batch count amplified into a much bigger — and
	// increasingly provider-dependent — number of unflushed chunks. A wall
	// clock bound is provider-agnostic and gives a predictable worst-case
	// data-loss window regardless of batch size. Run always flushes once
	// more at end-of-drain (and on every error/cancellation exit path)
	// regardless of this interval.
	flushInterval = 30 * time.Second

	// maxConsecutiveFailures is how many retries (with backoff) a batch gets
	// before it's treated as broken — enough to ride out a brief outage
	// without hanging forever on a truly broken embedder. For a batch of
	// more than one chunk, exhausting this budget triggers bisection
	// (embedChunksBisect) rather than aborting the whole Run — see there.
	maxConsecutiveFailures = 5

	// bisectAttempts/bisectRetryDelay govern retries for the sub-batches
	// embedChunksBisect tries while isolating a poison chunk. The top-level
	// batch already paid the full maxConsecutiveFailures/backoff cost before
	// bisection ever starts, so each split only needs a couple of quick
	// retries to rule out transient noise before splitting further or
	// quarantining — not another full exponential-backoff cycle.
	bisectAttempts   = 2
	bisectRetryDelay = 2 * time.Second

	defaultBackoffBase = 30 * time.Second
	maxBackoff         = 5 * time.Minute
)

// batchSizer is an optional capability an Embedder implementation can
// satisfy to report the largest batch it wants handed to a single Embed
// call (e.g. OpenAI's REST API accepts up to 100 inputs/request). EmbedWorker
// uses min(providerMax, configured) when BatchSize was explicitly configured,
// or providerMax on its own otherwise — see batchSize(). Embedders that don't
// implement this (e.g. Ollama, whose /api/embed batching characteristics
// aren't well-known enough to hardcode) keep falling back to BatchSize /
// defaultEmbedBatchSize exactly as before.
type batchSizer interface {
	MaxBatchSize() int
}

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

	// BisectDelay overrides the fixed delay embedChunksBisectAt uses between
	// retries of a sub-batch while isolating a poison chunk (tests only;
	// defaults to bisectRetryDelay). Unlike BackoffBase this never doubles —
	// see embedWithLimitedRetries.
	BisectDelay time.Duration

	notifyOnce sync.Once
	notifyCh   chan struct{}

	statusMu sync.Mutex
	status   WorkerStatus

	// quarantineMu/quarantined hold chunk IDs that embedChunksBisect isolated
	// as poison (repeatedly fail to embed on their own, non-systemically) —
	// see embedChunksBisect. Quarantine is in-memory and per-process by
	// design (CLAUDE.md-equivalent design note in the fix): a process
	// restart or a re-chunk that assigns the document a new chunk ID gets a
	// fresh chance. Guarded independently of statusMu since it's read from
	// the pending-list builder on every Run, not just from Status().
	quarantineMu sync.Mutex
	quarantined  map[int64]bool

	// wm* fields cache the state observed at the end of the last *fully
	// completed* global (Workspace=="") drain, so the next Run can cheaply
	// prove nothing changed instead of re-running the full chunk-ID/vector-ID
	// diff (see tryShortCircuit). Only ever read/written from Run, which is
	// never called concurrently with itself on the same EmbedWorker (RunLoop
	// drains sequentially; CLI callers construct one EmbedWorker per Run) —
	// no separate mutex needed.
	wmValid    bool
	wmMaxID    int64
	wmChunkCnt int
	wmVecCnt   int
}

// Worker states reported by Status(). "disabled" means no embedder is
// configured at all (Run/RunLoop are no-ops); "idle" means an embedder is
// configured but the worker currently has nothing to do (pending drained, or
// not yet started); "running" is an active drain; "backoff" is a drain
// paused between retries after a transient embed failure; "blocked_mismatch"
// is a drain that refused to proceed because the configured embedder's model
// doesn't match the one the existing index was built with (see
// ErrEmbedderMismatch) — recovering from this state requires `ney reset`, it
// does not clear on its own.
const (
	WorkerStateDisabled        = "disabled"
	WorkerStateIdle            = "idle"
	WorkerStateRunning         = "running"
	WorkerStateBackoff         = "backoff"
	WorkerStateBlockedMismatch = "blocked_mismatch"
)

// WorkerStatus is a snapshot of an EmbedWorker's progress, safe to read
// concurrently with Run/RunLoop via Status(). Done/Total describe the most
// recent (or in-progress) drain; both are 0 before the first Run call.
type WorkerStatus struct {
	State string
	Done  int
	Total int
}

// Status returns a snapshot of the worker's current state — intended for
// `ney mcp`'s index_status tool to poll cheaply without touching the DB or
// VectorStore itself.
func (w *EmbedWorker) Status() WorkerStatus {
	w.statusMu.Lock()
	defer w.statusMu.Unlock()
	return w.status
}

func (w *EmbedWorker) setState(state string) {
	w.statusMu.Lock()
	w.status.State = state
	w.statusMu.Unlock()
}

func (w *EmbedWorker) setProgress(done, total int) {
	w.statusMu.Lock()
	w.status.Done = done
	w.status.Total = total
	w.statusMu.Unlock()
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

// batchSize resolves how many chunks go into one Embed call: when the
// configured Embedder reports a capability via batchSizer, that's the
// ceiling — min(providerMax, w.BatchSize) if BatchSize was explicitly set,
// else providerMax outright (so e.g. OpenAI's 100/request default is actually
// used instead of the old flat 32). Embedders without the capability keep
// the pre-existing behavior: w.BatchSize if set, else defaultEmbedBatchSize.
func (w *EmbedWorker) batchSize() int {
	if bs, ok := w.Embedder.(batchSizer); ok {
		if max := bs.MaxBatchSize(); max > 0 {
			if w.BatchSize > 0 && w.BatchSize < max {
				return w.BatchSize
			}
			return max
		}
	}
	if w.BatchSize > 0 {
		return w.BatchSize
	}
	return defaultEmbedBatchSize
}

// quarantine marks id as poison for the rest of this process's lifetime:
// listChunkIDs' caller (Run) excludes it from every future pending list, and
// it's logged once here for operator visibility.
func (w *EmbedWorker) quarantine(id int64) {
	w.quarantineMu.Lock()
	if w.quarantined == nil {
		w.quarantined = make(map[int64]bool)
	}
	w.quarantined[id] = true
	w.quarantineMu.Unlock()
}

func (w *EmbedWorker) isQuarantined(id int64) bool {
	w.quarantineMu.Lock()
	defer w.quarantineMu.Unlock()
	return w.quarantined[id]
}

func (w *EmbedWorker) backoffBase() time.Duration {
	if w.BackoffBase > 0 {
		return w.BackoffBase
	}
	return defaultBackoffBase
}

func (w *EmbedWorker) bisectDelay() time.Duration {
	if w.BisectDelay > 0 {
		return w.BisectDelay
	}
	return bisectRetryDelay
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
		w.setState(WorkerStateDisabled)
		return nil
	}
	if err := w.checkModelConsistency(); err != nil {
		w.setState(WorkerStateBlockedMismatch)
		return err
	}
	global := w.Workspace == ""

	// Cheap short-circuit: if nothing has changed in the chunks table or the
	// vector store since the last fully-completed global drain, skip the
	// full paged chunk-ID scan + vector-ID diff entirely. See
	// tryShortCircuit for why this is safe (chunk IDs are AUTOINCREMENT and
	// never reused).
	if global && w.tryShortCircuit() {
		w.setState(WorkerStateIdle)
		w.setProgress(0, 0)
		return nil
	}

	w.setState(WorkerStateRunning)

	allChunkIDs, err := w.listChunkIDs()
	if err != nil {
		return fmt.Errorf("list chunk ids: %w", err)
	}

	// Call VectorStore.IDs() exactly once per Run and reuse the resulting
	// set both for the pending diff below and for cleanupOrphans (passed in
	// directly) instead of each recomputing it independently.
	vecIDs := w.Vectors.IDs()
	vecIDSet := make(map[string]bool, len(vecIDs))
	for _, id := range vecIDs {
		vecIDSet[id] = true
	}

	var pending []int64
	for _, id := range allChunkIDs {
		if vecIDSet[strconv.FormatInt(id, 10)] {
			continue
		}
		if w.isQuarantined(id) {
			// Poison chunk isolated by a previous drain's bisection — never
			// embeddable this process, don't keep retrying it.
			continue
		}
		pending = append(pending, id)
	}

	total := len(pending)
	if w.MaxChunks > 0 && w.MaxChunks < total {
		total = w.MaxChunks
	}

	bs := w.batchSize()
	done := 0
	embedderRecorded := false
	lastFlush := time.Now()
	w.setProgress(done, total)

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

		// embedChunksBisect tries the whole batch first and only falls back
		// to isolating individual poison chunks (quarantining them) if the
		// batch as a whole can't be embedded — see its doc comment. It can
		// return a non-nil items slice *and* a non-nil error: everything
		// embeddable in this batch gets added before Run aborts on a
		// systemic failure, so already-completed work is never thrown away.
		items, err := w.embedChunksBisect(ctx, chunks)
		if len(items) > 0 {
			if addErr := w.Vectors.Add(ctx, items); addErr != nil {
				_ = w.Vectors.Flush()
				return fmt.Errorf("add vectors: %w", addErr)
			}
			if !embedderRecorded {
				if err := w.DB.SetActiveEmbedder(w.Embedder.ModelID(), w.Embedder.ModelID(), w.Embedder.Dimensions()); err != nil {
					_ = w.Vectors.Flush()
					return fmt.Errorf("record active embedder: %w", err)
				}
				embedderRecorded = true
			}
		}
		if err != nil {
			_ = w.Vectors.Flush()
			return err
		}

		done = end
		if time.Since(lastFlush) >= flushInterval {
			lastFlush = time.Now()
			if err := w.Vectors.Flush(); err != nil {
				return fmt.Errorf("flush vectors: %w", err)
			}
		}
		w.setProgress(done, total)
		if w.OnProgress != nil {
			w.OnProgress(done, total)
		}
	}

	if err := w.Vectors.Flush(); err != nil {
		return fmt.Errorf("flush vectors: %w", err)
	}

	if global {
		// Skipping the orphan sweep entirely when nothing changed is handled
		// by the short-circuit at the top of Run (it returns before this
		// point is ever reached). Once we get this far there was a full
		// diff, so always give cleanupOrphans a chance — it internally
		// no-ops when there's nothing to delete.
		remaining := len(pending) - done
		idleDrain := remaining == 0
		if err := w.cleanupOrphans(ctx, allChunkIDs, vecIDSet, idleDrain); err != nil {
			return err
		}

		if idleDrain {
			// Record the watermark for the next Run's short-circuit check.
			// allChunkIDs is sorted ascending (listChunkIDs pages in
			// increasing-id order), so its last element is the max chunk ID
			// observed by this drain.
			w.wmValid = true
			w.wmChunkCnt = len(allChunkIDs)
			if n := len(allChunkIDs); n > 0 {
				w.wmMaxID = allChunkIDs[n-1]
			} else {
				w.wmMaxID = 0
			}
			w.wmVecCnt = len(w.Vectors.IDs())
		} else {
			w.wmValid = false
		}
	}

	w.setState(WorkerStateIdle)
	return nil
}

// tryShortCircuit reports whether Run can skip its full pending diff (the
// paged GetChunkIDsPage scan of every chunk ID plus VectorStore.IDs()) this
// call, because nothing has changed since the watermark recorded by the
// previous fully-completed global drain (see the end of Run). It only
// applies in global (unscoped) mode — scoped Run calls always do the full
// diff, since a workspace-scoped worker's watermark would need to track
// cross-workspace state it doesn't otherwise look at.
//
// Correctness rests on one CLAUDE.md-documented fact: chunk IDs are
// AUTOINCREMENT and never reused. That means any DB change since the
// watermark falls into one of three cases, and every case is caught:
//   - a chunk inserted (new doc, re-chunk, etc.): its ID is greater than any
//     ID that existed at watermark time, so the cheap
//     GetChunkIDsPage(wmMaxID, 1) probe below returns a non-empty page;
//   - a chunk deleted with nothing inserted (doc removed): the total chunk
//     count (CountChunks, a single COUNT(*) query) no longer matches;
//   - a delete+insert pair (re-chunk): the insert half is caught by the
//     first case regardless of whether the count happens to net out even.
//
// A vector-store-only change (e.g. cleanupOrphans running from a *different*
// EmbedWorker instance, or a concurrent scoped Run adding vectors) is caught
// by the len(w.Vectors.IDs()) comparison. Any query error, or a watermark
// that was never set (fresh EmbedWorker, or the last drain didn't fully
// complete), falls through to the full diff — "when in doubt, full diff".
func (w *EmbedWorker) tryShortCircuit() bool {
	if !w.wmValid {
		return false
	}
	tail, err := w.DB.GetChunkIDsPage(w.wmMaxID, 1)
	if err != nil || len(tail) != 0 {
		return false
	}
	count, err := w.DB.CountChunks()
	if err != nil || count != w.wmChunkCnt {
		return false
	}
	if len(w.Vectors.IDs()) != w.wmVecCnt {
		return false
	}
	return true
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
		w.setState(WorkerStateBackoff)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			w.setState(WorkerStateRunning)
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nil, fmt.Errorf("embedding failed %d times in a row, giving up: %w", maxConsecutiveFailures, lastErr)
}

// embedChunksBisect embeds chunks, trying the whole slice as one batch first
// (via embedBatch, with its full retry/backoff budget). If that fails —
// after ruling out cancellation, which is propagated immediately — it
// bisects: recursively retries each half with a much smaller retry budget
// (bisectAttempts, fixed short delay) rather than paying another full
// exponential-backoff cycle per split, since the top-level batch already
// did that. Splitting continues down to individual chunks, which get
// quarantined (see quarantine) rather than retried forever once isolated —
// unless the failure looks systemic (auth/model-not-found — see
// isSystemicEmbedError), in which case bisection stops and the error
// propagates up to abort the whole Run instead: retrying every other chunk
// one-by-one against a broken embedder/credential would be pointless and
// slow.
//
// It always returns whatever it managed to embed, even when it also returns
// an error — Run adds partial items before propagating an abort, so a
// systemic failure discovered deep in one batch doesn't throw away
// successfully embedded chunks from earlier in the same batch.
func (w *EmbedWorker) embedChunksBisect(ctx context.Context, chunks []*store.Chunk) ([]vectorstore.VectorItem, error) {
	return w.embedChunksBisectAt(ctx, chunks, true)
}

// embedChunksBisectAt is embedChunksBisect's recursive implementation.
// topLevel is true only for the initial (whole-batch) call from Run's loop —
// that one gets the full embedBatch retry/backoff treatment, since it's a
// fresh batch that hasn't failed yet. Every split produced by bisecting a
// failed batch uses embedWithLimitedRetries instead: the parent batch
// already paid the full backoff cost, so each half only needs a couple of
// quick, fixed-delay retries to rule out transient noise before either
// succeeding, splitting further, or (once down to a single chunk)
// quarantining.
func (w *EmbedWorker) embedChunksBisectAt(ctx context.Context, chunks []*store.Chunk, topLevel bool) ([]vectorstore.VectorItem, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}

	var vecs [][]float32
	var err error
	if topLevel {
		vecs, err = w.embedBatch(ctx, texts)
	} else {
		vecs, err = w.embedWithLimitedRetries(ctx, texts, bisectAttempts, w.bisectDelay())
	}
	if err == nil {
		items := make([]vectorstore.VectorItem, 0, len(chunks))
		for i, c := range chunks {
			if i < len(vecs) {
				items = append(items, vectorstore.VectorItem{
					ID:     strconv.FormatInt(c.ID, 10),
					Vector: vecs[i],
				})
			}
		}
		return items, nil
	}

	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}

	// Check for a systemic-looking failure (bad credentials, wrong model,
	// ...) *before* bisecting further, at whatever size this batch is — not
	// just at a single-chunk leaf. Otherwise a genuinely broken embedder
	// (every chunk fails identically) would pointlessly bisect all the way
	// down to individual chunks, burning through bisectAttempts retries at
	// every split, before finally recognizing the same systemic error at
	// the last leaf. Recognizing it as early as the first failed batch is
	// both faster and exactly as correct, since a systemic error being
	// systemic doesn't depend on how big the failing batch was.
	if isSystemicEmbedError(err) {
		return nil, fmt.Errorf("embedding failed with what looks like a systemic error, aborting: %w", err)
	}

	if len(chunks) == 1 {
		fmt.Fprintf(os.Stderr, "warning: quarantining chunk %d after repeated embed failures, will not retry this process: %v\n",
			chunks[0].ID, err)
		w.quarantine(chunks[0].ID)
		return nil, nil
	}

	mid := len(chunks) / 2
	leftItems, lerr := w.embedChunksBisectAt(ctx, chunks[:mid], false)
	if lerr != nil {
		return leftItems, lerr
	}
	rightItems, rerr := w.embedChunksBisectAt(ctx, chunks[mid:], false)
	return append(leftItems, rightItems...), rerr
}

// embedWithLimitedRetries is embedChunksBisectAt's retry helper for
// sub-batches produced while isolating a poison chunk — see its doc
// comment for why this uses a much smaller, fixed-delay budget than
// embedBatch's exponential backoff.
func (w *EmbedWorker) embedWithLimitedRetries(ctx context.Context, texts []string, attempts int, delay time.Duration) ([][]float32, error) {
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
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
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

// isSystemicEmbedError does a best-effort, provider-agnostic classification
// of an embed error as "the whole embedder looks broken" (bad/expired
// credentials, wrong model name) rather than "this specific input is
// unembeddable" (the poison-chunk case bisection exists to isolate). It's
// deliberately conservative — a false negative just means one more chunk
// gets quarantined instead of aborting the Run, which is the safer failure
// mode; a false positive would wrongly abort a Run that bisection could
// otherwise have completed around a handful of bad chunks.
func isSystemicEmbedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	systemicPatterns := []string{
		"401", "403", "unauthorized", "invalid api key", "invalid_api_key",
		"authentication", "forbidden", "permission denied",
		"model not found", "does not exist", "no such model",
	}
	for _, p := range systemicPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
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
// ever runs in unscoped mode, enforced by the caller). vecIDSet is the same
// set Run already built from a single w.Vectors.IDs() call at the top of
// this drain — nothing added during the drain itself can ever be an orphan
// (it was only added because a matching chunk row exists), so reusing the
// pre-drain snapshot here instead of calling IDs() again is correct, not
// just cheaper. Per the design doc (§4.2), HNSWStore.Delete unconditionally
// dirties the whole graph regardless of how many IDs are removed, forcing a
// full rebuild on the next Search — so this batches all orphans into one
// Delete call, and only bothers when there's a real backlog (>=
// orphanBatchThreshold) or when pending has fully drained (idleDrain),
// matching the "batch and defer" rule from the spec.
func (w *EmbedWorker) cleanupOrphans(ctx context.Context, allChunkIDs []int64, vecIDSet map[string]bool, idleDrain bool) error {
	chunkIDSet := make(map[string]bool, len(allChunkIDs))
	for _, id := range allChunkIDs {
		chunkIDSet[strconv.FormatInt(id, 10)] = true
	}

	var orphans []string
	for vid := range vecIDSet {
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
			// A mismatch is sticky by design (§4.2: "ไม่ auto-reset") — Run
			// already set WorkerStateBlockedMismatch, and status should keep
			// reporting that until a `ney reset` changes the underlying
			// model, not flip back to idle just because RunLoop is about to
			// wait for the next Notify. Any other error (e.g. embedder gave
			// up after repeated failures) does go back to idle: the next
			// Notify is a fresh attempt worth reporting as such.
			var mismatch *ErrEmbedderMismatch
			if !errors.As(err, &mismatch) {
				w.setState(WorkerStateIdle)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.notifyCh:
		}
	}
}
