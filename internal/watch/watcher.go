package watch

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/naay99999/neything/internal/index"
)

// skipDirNames are directory basenames addRecursive never registers an
// fsnotify watch on (and never descends into), on top of the existing
// dot-directory skip. These are the well-known dependency/build output
// directories that can contain hundreds of thousands of files with no
// value to the index — watching them wastes fsnotify's per-directory kernel
// resources (inotify watch descriptors on Linux, kqueue FDs on macOS) and
// makes every build/install/compile a storm of debounced flush work.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".next":        true,
	"out":          true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
}

type Stats struct {
	FilesIndexed  int
	FilesRemoved  int
	VectorsPruned int
	Errors        int
}

type Watcher struct {
	Indexer     *index.Indexer
	RootPath    string
	WorkspaceID int64
	Debounce    time.Duration
	SyncEvery   time.Duration
	OnEvent     func(msg string)
	// OnFlush, if set, is called after each debounced batch of filesystem
	// events has been processed (indexed/removed) — e.g. to nudge an
	// EmbedWorker (Notify) that new chunks may be pending. It fires even
	// when the batch turned out to be a no-op; it must be cheap.
	OnFlush func()
	// Serialize, if set, wraps every Indexer invocation made by the watcher
	// (flush batches and PruneMissing calls) so a caller embedding multiple
	// Watchers — e.g. the `ney mcp` server — can run them under a shared
	// mutex alongside its own indexing work. When nil, Indexer calls run
	// directly with no external synchronization (current/default behavior).
	Serialize func(run func())
	// DisablePrune, when true, turns off the periodic PruneMissing ticker
	// (governed by SyncEvery) as well as the final prune performed on
	// shutdown. Use this when an external owner (e.g. a server centralizing
	// prune across many watched roots) already handles pruning.
	DisablePrune bool
}

// Run watches RootPath for filesystem changes and indexes them until ctx is
// canceled. It installs no signal handlers of its own — callers that want
// Ctrl+C to stop a Run loop must derive ctx from a signal-aware context
// (e.g. signal.NotifyContext) themselves.
func (w *Watcher) Run(ctx context.Context) (*Stats, error) {
	if w.Debounce <= 0 {
		w.Debounce = 2 * time.Second
	}
	if w.SyncEvery <= 0 {
		w.SyncEvery = 5 * time.Minute
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	defer fsw.Close()

	if err := addRecursive(fsw, w.RootPath); err != nil {
		return nil, err
	}

	stats := &Stats{}
	pending := make(map[string]struct{})
	var pendingMu sync.Mutex

	runSerialized := func(run func()) {
		if w.Serialize != nil {
			w.Serialize(run)
		} else {
			run()
		}
	}

	flush := func() {
		pendingMu.Lock()
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = make(map[string]struct{})
		pendingMu.Unlock()

		if len(paths) == 0 {
			if w.OnFlush != nil {
				w.OnFlush()
			}
			return
		}

		runSerialized(func() {
			for _, path := range paths {
				if _, err := os.Stat(path); err != nil {
					s, err := w.Indexer.RemovePath(ctx, path)
					if err != nil {
						stats.Errors++
						w.logf("warning: remove %s: %v", path, err)
						continue
					}
					stats.FilesRemoved += s.FilesRemoved
					stats.VectorsPruned += s.VectorsPruned
					w.logf("removed %s", path)
					continue
				}
				if !index.IsSupportedExt(path) {
					continue
				}
				s, err := w.Indexer.IndexPath(ctx, path, w.WorkspaceID, "")
				if err != nil {
					stats.Errors++
					w.logf("warning: index %s: %v", path, err)
					continue
				}
				stats.FilesIndexed += s.FilesScanned - s.FilesSkipped
				stats.VectorsPruned += s.VectorsPruned
				if s.FilesSkipped > 0 {
					w.logf("skipped %s (unchanged)", path)
				} else {
					w.logf("indexed %s", path)
				}
			}
		})
		if w.OnFlush != nil {
			w.OnFlush()
		}
	}

	// pruneTick runs the periodic sweep for files removed without a
	// filesystem event (e.g. deleted while the watcher wasn't running).
	pruneTick := func() {
		runSerialized(func() {
			if s, err := w.Indexer.PruneMissing(ctx, w.RootPath, w.WorkspaceID); err != nil {
				stats.Errors++
				w.logf("warning: prune sync: %v", err)
			} else if s.FilesRemoved > 0 {
				stats.FilesRemoved += s.FilesRemoved
				stats.VectorsPruned += s.VectorsPruned
				w.logf("pruned %d missing files", s.FilesRemoved)
			}
		})
	}

	// pruneFinal runs once on shutdown, silently folding results into stats.
	pruneFinal := func() {
		runSerialized(func() {
			if s, err := w.Indexer.PruneMissing(ctx, w.RootPath, w.WorkspaceID); err == nil {
				stats.FilesRemoved += s.FilesRemoved
				stats.VectorsPruned += s.VectorsPruned
			}
		})
	}

	var debounceTimer *time.Timer
	var debounceMu sync.Mutex
	scheduleFlush := func() {
		debounceMu.Lock()
		defer debounceMu.Unlock()
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(w.Debounce, flush)
	}

	// syncTickerC stays nil (and so never fires) when pruning is disabled —
	// an external owner is expected to centralize pruning across watchers.
	var syncTickerC <-chan time.Time
	if !w.DisablePrune {
		syncTicker := time.NewTicker(w.SyncEvery)
		defer syncTicker.Stop()
		syncTickerC = syncTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			// Stop the pending debounce timer explicitly: without this, a
			// timer scheduled just before shutdown could still fire flush()
			// after Run has returned and the caller has moved on to closing
			// the DB/Vectors — racing a write against Close().
			debounceMu.Lock()
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceMu.Unlock()
			flush()
			if !w.DisablePrune {
				pruneFinal()
			}
			return stats, nil
		case <-syncTickerC:
			pruneTick()
		case err, ok := <-fsw.Errors:
			if !ok {
				return stats, nil
			}
			stats.Errors++
			w.logf("warning: watcher error: %v", err)
		case ev, ok := <-fsw.Events:
			if !ok {
				return stats, nil
			}
			if ev.Op&fsnotify.Create == fsnotify.Create {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && !skipDirNames[filepath.Base(ev.Name)] {
					_ = addRecursive(fsw, ev.Name)
				}
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				pendingMu.Lock()
				pending[ev.Name] = struct{}{}
				pendingMu.Unlock()
				scheduleFlush()
			}
		}
	}
}

func addRecursive(fsw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (d.Name()[0] == '.' || skipDirNames[d.Name()]) {
				return filepath.SkipDir
			}
			return fsw.Add(path)
		}
		return nil
	})
}

func (w *Watcher) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if w.OnEvent != nil {
		w.OnEvent(msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}
}
