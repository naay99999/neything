package lockfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
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

func TestAcquireStealsStaleLock(t *testing.T) {
	dir := t.TempDir()

	// Simulate a dead process's leftover lock file: an implausible pid.
	stale := lockInfo{PID: 99999999, Command: "ney mcp"}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0o644); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire should have stolen the stale lock: %v", err)
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

