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
