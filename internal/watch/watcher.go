package watch

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/naay99999/neything/internal/index"
)

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
}

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
	flush := func() {
		pendingMu.Lock()
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = make(map[string]struct{})
		pendingMu.Unlock()

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
		if w.OnFlush != nil {
			w.OnFlush()
		}
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	syncTicker := time.NewTicker(w.SyncEvery)
	defer syncTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			flush()
			if s, err := w.Indexer.PruneMissing(ctx, w.RootPath, w.WorkspaceID); err == nil {
				stats.FilesRemoved += s.FilesRemoved
				stats.VectorsPruned += s.VectorsPruned
			}
			return stats, nil
		case <-sigCh:
			w.logf("shutting down...")
			cancel()
		case <-syncTicker.C:
			if s, err := w.Indexer.PruneMissing(ctx, w.RootPath, w.WorkspaceID); err != nil {
				stats.Errors++
				w.logf("warning: prune sync: %v", err)
			} else if s.FilesRemoved > 0 {
				stats.FilesRemoved += s.FilesRemoved
				stats.VectorsPruned += s.VectorsPruned
				w.logf("pruned %d missing files", s.FilesRemoved)
			}
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
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
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
			if path != root && d.Name()[0] == '.' {
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
