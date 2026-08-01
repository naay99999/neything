// Package lockfile implements a cross-process writer lock backed by a
// kernel advisory lock (flock(2)) on a single file (~/.ney/writer.lock). It
// exists because both vectorstore backends flush by rewriting their file
// wholesale from memory: two writer processes (e.g. `ney mcp` and a
// concurrent `ney index`/`ney reset`) racing that flush would silently
// clobber each other's work.
//
// Exclusion comes from the kernel, not from a pid heuristic. An earlier
// version created the file with O_CREATE|O_EXCL and decided staleness by
// probing the recorded pid with signal 0; that had two holes. Pid reuse
// meant a dead holder whose pid had been recycled to an unrelated live
// process wedged the lock forever, and the read-decide-remove-recreate
// sequence used to steal a stale lock was itself racy between acquirers.
// flock has neither problem: the kernel drops the lock when the holding
// process dies (including SIGKILL and panics), so there is no staleness
// heuristic left to get wrong. The file's pid/command JSON survives only as
// a human-readable label for LockedError — it is never consulted to decide
// whether the lock is held.
//
// flock is per open file description, so a second Acquire *within the same
// process* is refused too (each Acquire opens its own descriptor). That is
// relied on by tests that simulate a competing writer in-process. Go opens
// files O_CLOEXEC, so the lock is not leaked to child processes.
//
// The file is unlinked on Release, so ~/.ney/writer.lock is absent whenever
// nothing holds it. Unlinking a lock file is the classic way to reintroduce
// a race — see acquire and Release for the two rules that make it safe.
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// fileName is the lock file's name within the given directory.
const fileName = "writer.lock"

// maxAttempts bounds the retry loop in acquire. Each retry costs one open +
// one flock and only happens when a releasing holder unlinked the file out
// from under us, which cannot repeat indefinitely in practice.
const maxAttempts = 5

// ErrLocked is a sentinel other packages can match with errors.Is. The
// concrete error returned by Acquire is always a *LockedError, which wraps
// this sentinel and carries the current holder's pid/command so callers can
// print a specific, actionable message.
var ErrLocked = errors.New("writer lock held by another ney process")

// errRaced signals that a competing holder replaced the lock file between
// our open and our flock, so the descriptor we locked is a detached inode
// nobody can see. Internal to acquire's retry loop; never returned to
// callers.
var errRaced = errors.New("lock file replaced during acquire")

// LockedError reports that the writer lock is held by another live process.
type LockedError struct {
	PID     int
	Command string
}

func (e *LockedError) Error() string {
	cmd := e.Command
	if cmd == "" {
		cmd = "unknown command"
	}
	return fmt.Sprintf("another ney process (pid %d, %s) is writing — stop it first", e.PID, cmd)
}

func (e *LockedError) Is(target error) bool { return target == ErrLocked }

// lockInfo is the JSON payload written into the lock file. It is purely
// descriptive: the flock, not this struct, decides who holds the lock.
type lockInfo struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Command   string    `json:"command"`
}

// Lock represents a held writer lock. Release it (typically via defer) when
// the writer is done so other processes can acquire it. The open descriptor
// is what actually holds the flock, so a Lock must stay reachable for as
// long as the writer runs — dropping it on the floor would let the GC close
// the file and silently release the lock.
type Lock struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// Acquire creates (or reuses) dir/writer.lock and takes an exclusive flock
// on it, claiming the process-wide writer lock. dir is normally
// config.NeyDir() (~/.ney).
//
// If another process (or another Acquire in this process) already holds the
// lock, Acquire returns a *LockedError describing the holder — it never
// blocks. A holder that died without releasing does not wedge the lock: the
// kernel dropped its flock, and the leftover file is simply reused.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, fileName)

	for i := 0; i < maxAttempts; i++ {
		lock, err := acquire(path)
		if !errors.Is(err, errRaced) {
			return lock, err
		}
	}
	// Lost the create/unlink race repeatedly — report whoever holds it now
	// rather than spinning.
	return nil, lockedErrorFor(path)
}

// acquire performs one attempt: open, flock, verify, stamp.
//
// The verify step is what makes unlink-on-Release safe. Without it: we open
// the file while the holder still has it, the holder then releases (unlink
// + close), our flock on the now-detached inode succeeds, and a third
// process creates a fresh file and locks that — two "holders" at once. So
// after taking the flock we confirm the descriptor we locked is still the
// file at path; if it is not, we drop it and retry against the new file.
func acquire(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, lockedErrorFor(path)
		}
		// Anything else (e.g. a filesystem without flock support) is a real
		// failure: ney needs working file locks regardless, since SQLite
		// takes its own POSIX locks on index.db in the same directory.
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	if !sameFileAtPath(f, path) {
		f.Close() // drops the flock on the detached inode
		return nil, errRaced
	}

	if err := writeLockInfo(f); err != nil {
		// We are the verified holder, so removing the file is safe here.
		os.Remove(path)
		f.Close()
		return nil, err
	}
	return &Lock{path: path, f: f}, nil
}

// Release unlinks the lock file and closes the descriptor, dropping the
// flock. It is safe to call multiple times and on a nil *Lock.
//
// Two ordering rules matter. First, unlink before close: closing first
// would leave a window where another process can open, lock and verify the
// file we are about to delete, and our unlink would then strand it holding
// a lock nobody else can see. Second, a repeat Release must be a true
// no-op — blindly unlinking again could delete a *different* process's lock
// file that was created in the meantime, so the descriptor is cleared here
// and guards the second call.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.f
	if f == nil {
		return nil
	}
	l.f = nil

	var rmErr error
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		rmErr = err
	}
	closeErr := f.Close()
	if rmErr != nil {
		return rmErr
	}
	return closeErr
}

// sameFileAtPath reports whether f is still the file living at path. Any
// error (most often the file having been unlinked) counts as "no".
func sameFileAtPath(f *os.File, path string) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	pi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(fi, pi)
}

// writeLockInfo stamps the holder's identity into the file we hold. The
// payload is a few dozen bytes written in one call, so a concurrent reader
// sees either the old or the new record; a torn read just fails to parse
// and degrades to an unnamed holder in LockedError.
func writeLockInfo(f *os.File) error {
	data, err := json.Marshal(lockInfo{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		Command:   commandName(),
	})
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt(data, 0)
	return err
}

// lockedErrorFor builds the *LockedError for a lock we could not take,
// labelling it with the holder recorded in the file. If the file cannot be
// read or parsed (it vanished, or the holder has not stamped it yet), the
// error still satisfies errors.Is(err, ErrLocked) but names no holder.
func lockedErrorFor(path string) error {
	info, err := readLockInfo(path)
	if err != nil {
		return &LockedError{}
	}
	return &LockedError{PID: info.PID, Command: info.Command}
}

func readLockInfo(path string) (lockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockInfo{}, err
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return lockInfo{}, err
	}
	return info, nil
}

// commandName renders a short "<binary> <subcommand>" hint for the lock
// file and error messages, e.g. "ney mcp" or "ney index". It deliberately
// stops at the subcommand and drops further args/flags (which may include
// filesystem paths) to keep the message short and non-sensitive.
func commandName() string {
	base := filepath.Base(os.Args[0])
	if len(os.Args) > 1 {
		return base + " " + os.Args[1]
	}
	return base
}
