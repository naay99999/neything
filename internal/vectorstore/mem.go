package vectorstore

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"os"
	"sync"
)

type BruteForceStore struct {
	mu      sync.RWMutex
	items   []VectorItem
	norms   []float32      // norms[i] = |items[i].Vector|, cached for search
	idx     map[string]int // ID → position in items
	path    string
	unsaved bool
}

func NewBruteForceStore(path string) (*BruteForceStore, error) {
	s := &BruteForceStore{path: path, idx: make(map[string]int)}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	return s, nil
}

func (s *BruteForceStore) load() error {
	items, err := loadFlatVectors(s.path)
	if err != nil {
		return err
	}
	s.items = items
	s.norms = make([]float32, len(items))
	s.idx = make(map[string]int, len(items))
	for i, it := range items {
		s.norms[i] = norm(it.Vector)
		s.idx[it.ID] = i
	}
	return nil
}

func (s *BruteForceStore) Add(_ context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		if pos, exists := s.idx[item.ID]; exists {
			s.items[pos] = item
			s.norms[pos] = norm(item.Vector)
		} else {
			s.idx[item.ID] = len(s.items)
			s.items = append(s.items, item)
			s.norms = append(s.norms, norm(item.Vector))
		}
	}
	s.unsaved = true
	return nil
}

func (s *BruteForceStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if k <= 0 || len(s.items) == 0 {
		return nil, nil
	}
	if k > len(s.items) {
		k = len(s.items)
	}

	qNorm := norm(query)
	if qNorm == 0 {
		return nil, nil
	}

	h := &minHeap{}
	heap.Init(h)

	for i, item := range s.items {
		n := s.norms[i]
		if n == 0 {
			continue
		}
		score := dot(query, item.Vector) / (qNorm * n)
		if h.Len() < k {
			heap.Push(h, SearchResult{ID: item.ID, Score: score})
		} else if score > (*h)[0].Score {
			heap.Pop(h)
			heap.Push(h, SearchResult{ID: item.ID, Score: score})
		}
	}

	results := make([]SearchResult, h.Len())
	for i := len(results) - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(SearchResult)
	}
	return results, nil
}

func (s *BruteForceStore) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	del := make(map[string]bool, len(ids))
	for _, id := range ids {
		del[id] = true
	}
	filteredItems := s.items[:0]
	filteredNorms := s.norms[:0]
	for i, item := range s.items {
		if !del[item.ID] {
			filteredItems = append(filteredItems, item)
			filteredNorms = append(filteredNorms, s.norms[i])
		}
	}
	s.items = filteredItems
	s.norms = filteredNorms
	s.idx = make(map[string]int, len(s.items))
	for i, item := range s.items {
		s.idx[item.ID] = i
	}
	s.unsaved = true
	return nil
}

func (s *BruteForceStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.unsaved {
		return nil
	}
	if err := saveFlatVectors(s.path, s.items); err != nil {
		return err
	}
	s.unsaved = false
	return nil
}

func (s *BruteForceStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

func (s *BruteForceStore) Close() error { return s.Flush() }

func (s *BruteForceStore) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.idx))
	for id := range s.idx {
		ids = append(ids, id)
	}
	return ids
}

// math helpers

func dot(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float32
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}

func norm(v []float32) float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	return float32(math.Sqrt(float64(sum)))
}

// min-heap of SearchResult by Score (ascending = smallest score at top)

type minHeap []SearchResult

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].Score < h[j].Score }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(SearchResult)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
