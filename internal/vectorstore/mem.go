package vectorstore

import (
	"context"
	"container/heap"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
)

type BruteForceStore struct {
	mu    sync.RWMutex
	items []VectorItem
	path  string
}

func NewBruteForceStore(path string) (*BruteForceStore, error) {
	s := &BruteForceStore{path: path}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	return s, nil
}

func (s *BruteForceStore) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer f.Close()

	var items []VectorItem
	for {
		var idLen uint32
		if err := binary.Read(f, binary.LittleEndian, &idLen); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read id len: %w", err)
		}
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(f, idBytes); err != nil {
			return fmt.Errorf("read id: %w", err)
		}
		var dimCount uint32
		if err := binary.Read(f, binary.LittleEndian, &dimCount); err != nil {
			return fmt.Errorf("read dim count: %w", err)
		}
		vec := make([]float32, dimCount)
		if err := binary.Read(f, binary.LittleEndian, vec); err != nil {
			return fmt.Errorf("read vector: %w", err)
		}
		items = append(items, VectorItem{ID: string(idBytes), Vector: vec})
	}
	s.items = items
	return nil
}

func (s *BruteForceStore) save() error {
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, item := range s.items {
		idBytes := []byte(item.ID)
		binary.Write(f, binary.LittleEndian, uint32(len(idBytes)))
		f.Write(idBytes)
		binary.Write(f, binary.LittleEndian, uint32(len(item.Vector)))
		binary.Write(f, binary.LittleEndian, item.Vector)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *BruteForceStore) Add(_ context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// build index of existing IDs for deduplication
	idx := make(map[string]int, len(s.items))
	for i, it := range s.items {
		idx[it.ID] = i
	}
	for _, item := range items {
		if pos, exists := idx[item.ID]; exists {
			s.items[pos] = item
		} else {
			idx[item.ID] = len(s.items)
			s.items = append(s.items, item)
		}
	}
	return s.save()
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

	for _, item := range s.items {
		n := norm(item.Vector)
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
	filtered := s.items[:0]
	for _, item := range s.items {
		if !del[item.ID] {
			filtered = append(filtered, item)
		}
	}
	s.items = filtered
	return s.save()
}

func (s *BruteForceStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

func (s *BruteForceStore) Close() error { return nil }

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

func (h minHeap) Len() int            { return len(h) }
func (h minHeap) Less(i, j int) bool  { return h[i].Score < h[j].Score }
func (h minHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)         { *h = append(*h, x.(SearchResult)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
