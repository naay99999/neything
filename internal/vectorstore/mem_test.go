package vectorstore

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
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
	if err := store.Close(); err != nil {
		t.Fatal(err)
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

func TestBruteForceStoreIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vectors.bin")
	store, err := NewBruteForceStore(path)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	if ids := store.IDs(); len(ids) != 0 {
		t.Fatalf("expected empty store to have no IDs, got %v", ids)
	}

	items := []VectorItem{
		{ID: "1", Vector: []float32{1, 0, 0}},
		{ID: "2", Vector: []float32{0.9, 0.1, 0}},
		{ID: "3", Vector: []float32{0, 1, 0}},
	}
	if err := store.Add(ctx, items); err != nil {
		t.Fatal(err)
	}
	ids := store.IDs()
	if !sameStringSet(ids, []string{"1", "2", "3"}) {
		t.Fatalf("expected IDs [1 2 3], got %v", ids)
	}

	// mutating the returned slice must not affect the store
	ids[0] = "mutated"
	if again := store.IDs(); !sameStringSet(again, []string{"1", "2", "3"}) {
		t.Fatalf("expected store IDs unaffected by mutation, got %v", again)
	}

	if err := store.Delete(ctx, []string{"2"}); err != nil {
		t.Fatal(err)
	}
	if ids := store.IDs(); !sameStringSet(ids, []string{"1", "3"}) {
		t.Fatalf("expected IDs [1 3] after delete, got %v", ids)
	}
}

// TestParallelSearchMatchesSerial covers fix #3: above parallelSearchThreshold,
// Search shards the scan across goroutines. The sharded top-k must be
// identical (as a set, and in score order) to the plain serial scan.
func TestParallelSearchMatchesSerial(t *testing.T) {
	const (
		n   = parallelSearchThreshold + 137 // force the parallel path
		dim = 16
		k   = 10
	)
	rng := rand.New(rand.NewSource(42))
	items := make([]VectorItem, n)
	norms := make([]float32, n)
	for i := range items {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rng.Float32()*2 - 1
		}
		items[i] = VectorItem{ID: strconv.Itoa(i), Vector: vec}
		norms[i] = norm(vec)
	}
	query := make([]float32, dim)
	for j := range query {
		query[j] = rng.Float32()*2 - 1
	}
	qNorm := norm(query)

	serial := searchTopK(items, norms, query, qNorm, k)
	parallel := parallelSearchTopK(items, norms, query, qNorm, k)

	if len(serial) != len(parallel) {
		t.Fatalf("result length mismatch: serial=%d parallel=%d", len(serial), len(parallel))
	}
	for i := range serial {
		if serial[i].ID != parallel[i].ID || serial[i].Score != parallel[i].Score {
			t.Fatalf("mismatch at rank %d: serial=%+v parallel=%+v", i, serial[i], parallel[i])
		}
	}

	// Also exercise it end-to-end through BruteForceStore.Search, which
	// picks the parallel path automatically once len(items) crosses the
	// threshold.
	dir := t.TempDir()
	store, err := NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Add(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	viaStore, err := store.Search(context.Background(), query, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(viaStore) != len(serial) {
		t.Fatalf("store search length mismatch: got %d want %d", len(viaStore), len(serial))
	}
	for i := range serial {
		if viaStore[i].ID != serial[i].ID {
			t.Fatalf("store search mismatch at rank %d: got %+v want %+v", i, viaStore[i], serial[i])
		}
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}
