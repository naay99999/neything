package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/lockfile"
)

// TestIndexRefusesWhenWriterLockHeld exercises the full CLI path (matching
// M2.4 of the tiered-search plan): if another writer process already holds
// ~/.ney/writer.lock, `ney index` must fail fast with the documented
// "another ney process (pid X, cmd) is writing — stop it first" message
// instead of racing the lock holder's vectors-file flush.
func TestIndexRefusesWhenWriterLockHeld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "note.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate a concurrent writer (e.g. a long-lived `ney mcp`) already
	// holding the lock.
	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	resetAllFlags(rootCmd)
	defer resetAllFlags(rootCmd)

	rootCmd.SetArgs([]string{"index", corpus})
	err = rootCmd.Execute()
	if err == nil {
		t.Fatal("expected index to fail while the writer lock is held")
	}
	if !strings.Contains(err.Error(), "is writing — stop it first") {
		t.Fatalf("expected friendly writer-lock error, got: %v", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Fatalf("expected error to report the holder's pid (%d), got: %v", os.Getpid(), err)
	}
}
