package vectorstore

import (
	"context"
	"container/heap"
	"fmt"
	"os"
	"sync"

	"github.com/coder/hnsw"
)

type HNSWOptions struct {
	M              int
	EfConstruction int
	EfSearch       int
}

type HNSWStore struct {
	mu       sync.RWMutex
	items    map[string]VectorItem
	graph    *hnsw.Graph[string]
	path     string
	m        int
	efSearch int
	dirty    bool
}

func NewHNSWStore(path string, opts HNSWOptions) (*HNSWStore, error) {
	if opts.M <= 0 {
		opts.M = 16
	}
	if opts.EfSearch <= 0 {
		opts.EfSearch = 50
	}
	s := &HNSWStore{
		items:    make(map[string]VectorItem),
		path:     path,
		m:        opts.M,
		efSearch: opts.EfSearch,
		dirty:    true,
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load hnsw vectors: %w", err)
	}
	return s, nil
}

func (s *HNSWStore) load() error {
	items, err := loadFlatVectors(s.path)
	if err != nil {
		return err
	}
	s.items = itemsToMap(items)
	s.dirty = true
	return nil
}

func (s *HNSWStore) persistLocked() error {
	return saveFlatVectors(s.path, mapToItems(s.items))
}

func (s *HNSWStore) rebuildGraphLocked() {
	g := hnsw.NewGraph[string]()
	g.M = s.m
	g.EfSearch = s.efSearch
	g.Distance = hnsw.CosineDistance

	if len(s.items) > 0 {
		nodes := make([]hnsw.Node[string], 0, len(s.items))
		for _, item := range s.items {
			nodes = append(nodes, hnsw.MakeNode(item.ID, item.Vector))
		}
		g.Add(nodes...)
	}
	s.graph = g
	s.dirty = false
}

func (s *HNSWStore) Add(_ context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		s.items[item.ID] = item
	}
	s.dirty = true
	return s.persistLocked()
}

func (s *HNSWStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if k <= 0 || len(s.items) == 0 {
		return nil, nil
	}
	if k > len(s.items) {
		k = len(s.items)
	}
	if s.dirty || s.graph == nil {
		s.rebuildGraphLocked()
	}

	nodes := s.graph.Search(query, k)
	results := make([]SearchResult, 0, len(nodes))
	for _, node := range nodes {
		score := cosineSimilarity(query, node.Value)
		results = append(results, SearchResult{ID: node.Key, Score: score})
	}
	sortSearchResultsDesc(results)
	return results, nil
}

func (s *HNSWStore) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		delete(s.items, id)
	}
	s.dirty = true
	return s.persistLocked()
}

func (s *HNSWStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

func (s *HNSWStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistLocked()
}

func ImportBruteForceToHNSW(brutePath, hnswPath string, opts HNSWOptions) error {
	items, err := loadFlatVectors(brutePath)
	if err != nil {
		return err
	}
	s, err := NewHNSWStore(hnswPath, opts)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	return s.Add(context.Background(), items)
}

func sortSearchResultsDesc(results []SearchResult) {
	h := &searchMaxHeap{}
	heap.Init(h)
	for _, r := range results {
		heap.Push(h, r)
	}
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(SearchResult)
	}
}

type searchMaxHeap []SearchResult

func (h searchMaxHeap) Len() int            { return len(h) }
func (h searchMaxHeap) Less(i, j int) bool  { return h[i].Score < h[j].Score }
func (h searchMaxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *searchMaxHeap) Push(x any)         { *h = append(*h, x.(SearchResult)) }
func (h *searchMaxHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func cosineSimilarity(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float32
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (float32(sqrt(float64(na))) * float32(sqrt(float64(nb))))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}
