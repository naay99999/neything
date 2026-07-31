package vectorstore

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
)

// parallelSearchThreshold is the store size above which Search shards the
// scan across goroutines. Below it, goroutine setup overhead isn't worth
// it.
const parallelSearchThreshold = 5000

type BruteForceStore struct {
	mu    sync.RWMutex
	items []VectorItem
	norms []float32      // norms[i] = |items[i].Vector|, cached for search
	idx   map[string]int // ID → position in items
	flat  *flatFile
}

func NewBruteForceStore(path string) (*BruteForceStore, error) {
	s := &BruteForceStore{idx: make(map[string]int), flat: newFlatFile(path)}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	return s, nil
}

func (s *BruteForceStore) load() error {
	items, err := s.flat.load()
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
		s.flat.stageAdd(item)
	}
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

	if len(s.items) < parallelSearchThreshold {
		return searchTopK(s.items, s.norms, query, qNorm, k), nil
	}
	return parallelSearchTopK(s.items, s.norms, query, qNorm, k), nil
}

// searchTopK scans items[i] against query serially, returning the top k by
// cosine similarity in descending score order.
func searchTopK(items []VectorItem, norms []float32, query []float32, qNorm float32, k int) []SearchResult {
	h := &minHeap{}
	heap.Init(h)

	for i, item := range items {
		n := norms[i]
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
	return results
}

// parallelSearchTopK shards items across GOMAXPROCS-bounded goroutines,
// each computing a local top-k via searchTopK, then merges the shard
// results into a single top-k. Same result set as searchTopK (modulo
// arbitrary tie-break ordering on exactly-equal scores).
func parallelSearchTopK(items []VectorItem, norms []float32, query []float32, qNorm float32, k int) []SearchResult {
	workers := runtime.GOMAXPROCS(0)
	if workers > len(items) {
		workers = len(items)
	}
	if workers < 1 {
		workers = 1
	}
	shardSize := (len(items) + workers - 1) / workers

	shardResults := make([][]SearchResult, 0, workers)
	resultsCh := make(chan []SearchResult, workers)
	var wg sync.WaitGroup
	for start := 0; start < len(items); start += shardSize {
		end := start + shardSize
		if end > len(items) {
			end = len(items)
		}
		wg.Add(1)
		go func(items []VectorItem, norms []float32) {
			defer wg.Done()
			resultsCh <- searchTopK(items, norms, query, qNorm, k)
		}(items[start:end], norms[start:end])
	}
	wg.Wait()
	close(resultsCh)
	for r := range resultsCh {
		shardResults = append(shardResults, r)
	}

	merged := &minHeap{}
	heap.Init(merged)
	for _, shard := range shardResults {
		for _, r := range shard {
			if merged.Len() < k {
				heap.Push(merged, r)
			} else if r.Score > (*merged)[0].Score {
				heap.Pop(merged)
				heap.Push(merged, r)
			}
		}
	}

	out := make([]SearchResult, merged.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(merged).(SearchResult)
	}
	return out
}

func (s *BruteForceStore) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	del := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, exists := s.idx[id]; exists {
			del[id] = true
		}
	}
	if len(del) == 0 {
		return nil
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
	for id := range del {
		s.flat.stageDelete(id)
	}
	return nil
}

func (s *BruteForceStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flat.flush(func() []VectorItem { return s.items })
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
