// Package lockfile implements a simple advisory, cross-process writer lock
// backed by a single file (~/.ney/writer.lock). It exists because both
// vectorstore backends flush by rewriting their file wholesale from memory:
// two writer processes (e.g. `ney mcp` and a concurrent `ney index`/`ney
// reset`) racing that flush would silently clobber each other's work.
//
// The lock is advisory only — it does not use flock/fcntl, just an
// O_CREATE|O_EXCL file containing the holder's pid and command. It is stale
// if the recorded pid is no longer alive, in which case a new acquirer
// steals it.
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// fileName is the lock file's name within the given directory.
const fileName = "writer.lock"

// ErrLocked is a sentinel other packages can match with errors.Is. The
// concrete error returned by Acquire is always a *LockedError, which wraps
// this sentinel and carries the current holder's pid/command so callers can
// print a specific, actionable message.
var ErrLocked = errors.New("writer lock held by another ney process")

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

// lockInfo is the JSON payload written into the lock file.
type lockInfo struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"` // hint only; not used for staleness
	Command   string    `json:"command"`
}

// Lock represents a held writer lock. Release it (typically via defer) when
// the writer is done so other processes can acquire it.
type Lock struct {
	path string
}

// Acquire creates dir/writer.lock, claiming the process-wide writer lock.
// dir is normally config.NeyDir() (~/.ney).
//
// If a lock file already exists, Acquire checks whether its recorded pid is
// still alive. If the holder is dead, the lock is considered stale, removed,
// and acquisition is retried once. If the holder is alive (or the stale
// file was raced away by another acquirer), Acquire returns a *LockedError
// describing the current holder.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	return acquire(filepath.Join(dir, fileName), false)
}

func acquire(path string, retriedStale bool) (*Lock, error) {
	info := lockInfo{
		PID:       os.Getpid(),
		StartedAt: time.Now(),
		Command:   commandName(),
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}

		holder, readErr := readLockInfo(path)
		if readErr == nil && !processAlive(holder.PID) && !retriedStale {
			// Stale: the recorded process is no longer running. Steal the
			// lock and retry once (only once — if we still lose the race
			// against a fresh acquirer, report that holder as locked).
			_ = os.Remove(path)
			return acquire(path, true)
		}

		if readErr != nil {
			// Couldn't read/parse the competing lock file (e.g. a
			// concurrent writer, or it vanished). Report an unknown
			// holder rather than guessing.
			return nil, &LockedError{}
		}
		return nil, &LockedError{PID: holder.PID, Command: holder.Command}
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		os.Remove(path)
		return nil, err
	}
	return &Lock{path: path}, nil
}

// Release removes the lock file. It is safe to call multiple times or on a
// nil *Lock.
func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	err := os.Remove(l.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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

// processAlive reports whether pid refers to a live process, using signal 0
// (POSIX: "no signal sent, but error checking is still performed") — the
// standard portable way to probe liveness on darwin/linux without ptrace or
// /proc parsing. The project does not ship Windows builds, so this stays
// simple rather than adding a syscall.Kill build-tag shim.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means the process exists but is owned by someone else — still
	// alive. Anything else (typically ESRCH) means it's gone.
	return errors.Is(err, syscall.EPERM)
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
