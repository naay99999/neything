package vectorstore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestHNSWStoreAddSearchDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.hnsw")
	s, err := NewHNSWStore(path, HNSWOptions{M: 8, EfSearch: 20})
	if err != nil {
		t.Fatal(err)
	}

	items := []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0.9, 0.1, 0}},
		{ID: "3", Vector: []float32{0, 1, 0}},
	}
	if err := s.Add(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 3 {
		t.Fatalf("expected 3 vectors, got %d", s.Count())
	}

	results, err := s.Search(context.Background(), []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].ID != "1" {
		t.Fatalf("expected id 1 top result, got %+v", results)
	}

	if err := s.Delete(context.Background(), []string{"2"}); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("expected 2 vectors after delete, got %d", s.Count())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewHNSWStore(path, HNSWOptions{M: 8, EfSearch: 20})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 2 {
		t.Fatalf("expected persisted count 2, got %d", s2.Count())
	}
}

func TestImportBruteForceToHNSW(t *testing.T) {
	dir := t.TempDir()
	brutePath := filepath.Join(dir, "vectors.bin")
	hnswPath := filepath.Join(dir, "vectors.hnsw")

	brute, err := NewBruteForceStore(brutePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := brute.Add(context.Background(), []VectorItem{
		{ID: "10", Vector: []float32{0.5, 0.5, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := brute.Close(); err != nil {
		t.Fatal(err)
	}

	if err := ImportBruteForceToHNSW(brutePath, hnswPath, HNSWOptions{M: 8, EfSearch: 20}); err != nil {
		t.Fatal(err)
	}

	hnsw, err := NewHNSWStore(hnswPath, HNSWOptions{M: 8, EfSearch: 20})
	if err != nil {
		t.Fatal(err)
	}
	if hnsw.Count() != 1 {
		t.Fatalf("expected 1 imported vector, got %d", hnsw.Count())
	}
}

func TestHNSWStoreIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.hnsw")
	s, err := NewHNSWStore(path, HNSWOptions{M: 8, EfSearch: 20})
	if err != nil {
		t.Fatal(err)
	}

	if ids := s.IDs(); len(ids) != 0 {
		t.Fatalf("expected empty store to have no IDs, got %v", ids)
	}

	items := []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0.9, 0.1, 0}},
		{ID: "3", Vector: []float32{0, 1, 0}},
	}
	if err := s.Add(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	ids := s.IDs()
	if !sameStringSet(ids, []string{"1", "2", "3"}) {
		t.Fatalf("expected IDs [1 2 3], got %v", ids)
	}

	// mutating the returned slice must not affect the store
	ids[0] = "mutated"
	if again := s.IDs(); !sameStringSet(again, []string{"1", "2", "3"}) {
		t.Fatalf("expected store IDs unaffected by mutation, got %v", again)
	}

	if err := s.Delete(context.Background(), []string{"2"}); err != nil {
		t.Fatal(err)
	}
	if ids := s.IDs(); !sameStringSet(ids, []string{"1", "3"}) {
		t.Fatalf("expected IDs [1 3] after delete, got %v", ids)
	}
}

// TestHNSWDeleteThenSearchNeverReturnsDeletedIDs covers the fix-#2
// correctness requirement: a Search issued right after a Delete, before
// any Flush has had a chance to rebuild the graph off the query path,
// must never surface a deleted ID (the graph still physically contains
// the stale node until the next rebuild -- Search must filter it out).
func TestHNSWDeleteThenSearchNeverReturnsDeletedIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.hnsw")
	s, err := NewHNSWStore(path, HNSWOptions{M: 8, EfSearch: 20})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const n = 50
	items := make([]VectorItem, n)
	for i := range items {
		items[i] = VectorItem{ID: fmt.Sprintf("id-%d", i), Vector: unitVector(n, i)}
	}
	if err := s.Add(ctx, items); err != nil {
		t.Fatal(err)
	}

	// Delete most of the store without ever calling Flush: the graph is
	// now dirty and full of stale nodes, and no rebuild has happened.
	var deleted []string
	for i := 0; i < n-3; i++ {
		deleted = append(deleted, items[i].ID)
	}
	if err := s.Delete(ctx, deleted); err != nil {
		t.Fatal(err)
	}
	deletedSet := make(map[string]bool, len(deleted))
	for _, id := range deleted {
		deletedSet[id] = true
	}

	// Search for every surviving item's own vector (guaranteed nearest to
	// itself) with a generous k so the (still stale) graph has to return
	// deleted candidates too; none should leak through.
	for i := n - 3; i < n; i++ {
		results, err := s.Search(ctx, items[i].Vector, n)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if deletedSet[r.ID] {
				t.Fatalf("search returned deleted ID %q in results %+v", r.ID, results)
			}
		}
	}

	// A brand-new item added while still dirty must be findable
	// immediately, without waiting for a rebuild.
	newItem := VectorItem{ID: "brand-new", Vector: unitVector(n, 0)}
	if err := s.Add(ctx, []VectorItem{newItem}); err != nil {
		t.Fatal(err)
	}
	results, err := s.Search(ctx, newItem.Vector, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.ID == "brand-new" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected item added while dirty to be immediately findable, got %+v", results)
	}

	// Flush rebuilds the graph off the query path; afterwards the store
	// should be clean and consistent.
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 4 {
		t.Fatalf("expected 4 survivors after delete+add, got %d", s.Count())
	}
	results, err = s.Search(ctx, newItem.Vector, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if deletedSet[r.ID] {
			t.Fatalf("post-flush search returned deleted ID %q", r.ID)
		}
	}
}

// unitVector returns a dim-length vector that's non-zero only at index i%dim,
// giving each item a distinct, easily-nearest-to-itself direction.
func unitVector(dim, i int) []float32 {
	v := make([]float32, dim)
	v[i%dim] = 1
	return v
}
