package vectorstore

import (
	"context"
	"math"
	"path/filepath"
	"strconv"
	"testing"
)

// BenchmarkBruteForce_vs_HNSW at ~10k vectors (dim 384).
// Expect HNSW search to be significantly faster than brute-force scan.
func BenchmarkVectorSearch(b *testing.B) {
	const (
		n   = 10000
		dim = 384
		k   = 8
	)

	items := make([]VectorItem, n)
	for i := range items {
		vec := make([]float32, dim)
		seed := uint64(i + 1)
		for j := range vec {
			seed = seed*6364136223846793005 + 1
			vec[j] = float32(int(seed>>33)%1000)/500 - 1
		}
		normalize(vec)
		items[i] = VectorItem{ID: strconv.Itoa(i), Vector: vec}
	}
	query := items[500].Vector

	dir := b.TempDir()
	brutePath := filepath.Join(dir, "vectors.bin")
	hnswPath := filepath.Join(dir, "vectors.hnsw")

	brute, err := NewBruteForceStore(brutePath)
	if err != nil {
		b.Fatal(err)
	}
	if err := brute.Add(context.Background(), items); err != nil {
		b.Fatal(err)
	}

	hnsw, err := NewHNSWStore(hnswPath, HNSWOptions{M: 16, EfSearch: 50})
	if err != nil {
		b.Fatal(err)
	}
	if err := hnsw.Add(context.Background(), items); err != nil {
		b.Fatal(err)
	}

	b.Run("BruteForce", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := brute.Search(context.Background(), query, k); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("HNSW", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := hnsw.Search(context.Background(), query, k); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func normalize(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(float64(sum)))
	for i := range v {
		v[i] *= inv
	}
}
