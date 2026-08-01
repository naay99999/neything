package vectorstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func modePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// Vector files hold the embeddings of the user's private documents, so
// every write path (append, compacting rewrite) must leave them 0600 — the
// 0700 parent directory is not the only layer we rely on.
func TestFlatFileWrittenPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.bin")
	s, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Add(ctx, []VectorItem{{ID: "1", Vector: []float32{1, 0}}, {ID: "2", Vector: []float32{0, 1}}}); err != nil {
		t.Fatal(err)
	}
	// Append path (file created fresh by appendFlatRecords).
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := modePerm(t, path); got != 0o600 {
		t.Fatalf("after append flush: mode = %04o, want 0600", got)
	}

	// Compacting rewrite path (writeFlatSnapshot: tmp file + rename, which
	// carries the tmp file's mode over).
	if err := s.Delete(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := modePerm(t, path); got != 0o600 {
		t.Fatalf("after compacting flush: mode = %04o, want 0600", got)
	}
}

// A vector file left world-readable by an older ney is repaired on load.
func TestFlatFileLoadTightensExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.bin")
	s, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(context.Background(), []VectorItem{{ID: "1", Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	s2, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := modePerm(t, path); got != 0o600 {
		t.Fatalf("after reload: mode = %04o, want 0600", got)
	}
	if s2.Count() != 1 {
		t.Fatalf("expected 1 item after reload, got %d", s2.Count())
	}
}

// The HNSW graph cache embeds every vector too, so it gets the same mode as
// the flat file it caches.
func TestHNSWFilesWrittenPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.hnsw")
	s, err := NewHNSWStore(path, HNSWOptions{M: 8, EfSearch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(context.Background(), []VectorItem{{ID: "1", Vector: []float32{1, 0}}, {ID: "2", Vector: []float32{0, 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := modePerm(t, path); got != 0o600 {
		t.Fatalf("vector file mode = %04o, want 0600", got)
	}
	if got := modePerm(t, path+".graph"); got != 0o600 {
		t.Fatalf("graph cache mode = %04o, want 0600", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
