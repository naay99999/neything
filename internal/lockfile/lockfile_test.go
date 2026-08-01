package lockfile

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireReleaseRoundtrip(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("lock file still present after Release: err=%v", err)
	}
}

func TestAcquireSecondFailsWithHolderInfo(t *testing.T) {
	dir := t.TempDir()

	lock1, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lock1.Release()

	_, err = Acquire(dir)
	if err == nil {
		t.Fatal("expected second Acquire to fail")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("expected errors.Is(err, ErrLocked), got %v", err)
	}
	var lockedErr *LockedError
	if !errors.As(err, &lockedErr) {
		t.Fatalf("expected *LockedError, got %T: %v", err, err)
	}
	if lockedErr.PID != os.Getpid() {
		t.Errorf("holder PID = %d, want current pid %d", lockedErr.PID, os.Getpid())
	}
	if lockedErr.Command == "" {
		t.Error("holder Command is empty")
	}
	if got := err.Error(); got == "" {
		t.Error("empty error message")
	} else {
		t.Logf("message: %s", got)
	}
}

// TestAcquireIgnoresLeftoverLockFile pins the post-flock semantics: a lock
// FILE left behind by a crashed holder carries no authority at all, because
// the lock lives in the kernel (flock on the open fd), not in the file's
// contents. The pid inside is a human-readable label only.
//
// This test predates the flock rewrite, when it was named
// ...StealsStaleLock and exercised a pid-liveness probe (syscall.Kill(pid, 0))
// plus a remove-and-retry steal. That heuristic is gone — a recycled pid can
// no longer wedge the lock, and a killed holder is released by the kernel
// immediately. The invariant being asserted is unchanged: a leftover file
// must never block acquisition.
func TestAcquireIgnoresLeftoverLockFile(t *testing.T) {
	dir := t.TempDir()

	// A crashed holder's leftover file, naming a pid that is not running.
	stale := lockInfo{PID: 99999999, Command: "ney mcp"}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
		t.Fatalf("write leftover lock: %v", err)
	}

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire should ignore an unlocked leftover file: %v", err)
	}
	defer lock.Release()

	got, err := readLockInfo(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("readLockInfo: %v", err)
	}
	if got.PID != os.Getpid() {
		t.Errorf("lock file pid = %d, want current pid %d", got.PID, os.Getpid())
	}
}

func TestReleaseIdempotent(t *testing.T) {
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release should be a no-op, got: %v", err)
	}

	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("Release on nil *Lock should be a no-op, got: %v", err)
	}
}

func TestReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	lock1, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	lock2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release should succeed, got: %v", err)
	}
	defer lock2.Release()
}

// TestReleaseTwiceKeepsNewHoldersLock guards the unlink race that comes
// with removing the lock file on Release: a second Release must not delete
// the file a *later* acquirer created, or a third process would create yet
// another file and two writers would run at once.
func TestReleaseTwiceKeepsNewHoldersLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)

	stale, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := stale.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	holder, err := Acquire(dir)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer holder.Release()

	if err := stale.Release(); err != nil {
		t.Fatalf("repeat Release: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("repeat Release removed the current holder's lock file: %v", err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock should still be held after a repeat Release, got: %v", err)
	}
}

// TestKilledHolderReleasesLock is the property the pid-liveness check could
// never guarantee: a holder that dies without releasing (SIGKILL, panic,
// crash) leaves its file behind, but the kernel drops the flock, so the
// next acquirer takes it immediately — no staleness heuristic, no pid-reuse
// hole. The subprocess is this test binary re-run against the helper below.
func TestKilledHolderReleasesLock(t *testing.T) {
	dir := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=TestHoldLockHelper", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), helperEnv+"="+dir)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("helper did not report: %v (got %q)", err, line)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, helperReady)))
	if err != nil {
		t.Fatalf("unexpected helper output %q: %v", line, err)
	}

	// While the child lives, we must be refused and told who holds it.
	_, err = Acquire(dir)
	var lockedErr *LockedError
	if !errors.As(err, &lockedErr) {
		t.Fatalf("expected *LockedError while helper holds the lock, got %T: %v", err, err)
	}
	if lockedErr.PID != childPID {
		t.Errorf("holder PID = %d, want helper pid %d", lockedErr.PID, childPID)
	}

	// SIGKILL: no Release runs, so the lock file is still on disk.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_, _ = cmd.Process.Wait()
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("expected the killed holder's lock file to remain: %v", err)
	}

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after the holder was killed: %v", err)
	}
	defer lock.Release()
}

// TestConcurrentAcquireIsExclusive hammers the acquire/Release cycle from
// several goroutines. Each goroutine opens its own descriptor, so flock
// arbitrates for real; the point is to shake out the window where a
// releasing holder unlinks the file while another acquirer already has it
// open, which acquire's inode check must turn into a retry rather than a
// second simultaneous holder.
func TestConcurrentAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()

	var held atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				lock, err := Acquire(dir)
				if err != nil {
					if !errors.Is(err, ErrLocked) {
						t.Errorf("Acquire: %v", err)
						return
					}
					continue
				}
				if n := held.Add(1); n != 1 {
					t.Errorf("%d simultaneous lock holders", n)
				}
				held.Add(-1)
				if err := lock.Release(); err != nil {
					t.Errorf("Release: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

const (
	helperEnv   = "NEY_LOCKFILE_HELPER_DIR"
	helperReady = "locked "
)

// TestHoldLockHelper is not a test: it is the subprocess entry point for
// TestKilledHolderReleasesLock, which re-executes this binary with
// NEY_LOCKFILE_HELPER_DIR set. It takes the lock, announces its pid, and
// waits to be killed.
func TestHoldLockHelper(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("subprocess helper; only runs with " + helperEnv + " set")
	}
	if _, err := Acquire(dir); err != nil {
		t.Fatalf("helper Acquire: %v", err)
	}
	if _, err := os.Stdout.WriteString(helperReady + strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		t.Fatalf("helper announce: %v", err)
	}
	// Held until the parent kills us. The timeout is a backstop so a failed
	// parent cannot leave this process behind forever.
	time.Sleep(60 * time.Second)
}
