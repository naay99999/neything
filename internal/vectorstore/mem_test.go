package vectorstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBruteForceStoreAddSearchDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	items := []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0.9, 0.1, 0}},
		{ID: "3", Vector: []float32{0, 1, 0}},
	}
	if err := store.Add(ctx, items); err != nil {
		t.Fatal(err)
	}

	results, err := store.Search(ctx, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "1" {
		t.Fatalf("expected top result ID 1, got %s", results[0].ID)
	}

	if err := store.Delete(ctx, []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 2 {
		t.Fatalf("expected count 2 after delete, got %d", store.Count())
	}

	// reload from disk
	store2, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store2.Count() != 2 {
		t.Fatalf("expected persisted count 2, got %d", store2.Count())
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
