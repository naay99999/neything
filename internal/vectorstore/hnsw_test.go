package vectorstore

import (
	"context"
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
