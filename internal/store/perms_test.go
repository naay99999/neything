package store

import (
	"os"
	"path/filepath"
	"testing"
)

// index.db holds the full text of every indexed document, so it must never
// be created world-readable — ~/.ney being 0700 is one layer, and a backup
// restore / rsync / bind-mount can loosen it. Same for the WAL sidecars,
// which hold recently written pages of that same text.
func TestOpenCreatesPrivateDBFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("index.db mode = %04o, want 0600", got)
	}

	// Force WAL traffic so the sidecars definitely exist, then re-check.
	if _, err := db.UpsertWorkspace("test", dir); err != nil {
		t.Fatal(err)
	}
	tightenDBPerms(dbPath)
	for _, suffix := range []string{"-wal", "-shm"} {
		si, err := os.Stat(dbPath + suffix)
		if os.IsNotExist(err) {
			continue // sidecar not materialized on this platform/build
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := si.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %04o, want 0600", suffix, got)
		}
	}
}

// A chmod failure (or a missing sidecar) must never be fatal.
func TestTightenDBPermsToleratesMissingFiles(t *testing.T) {
	tightenDBPerms(filepath.Join(t.TempDir(), "does-not-exist.db"))
}
