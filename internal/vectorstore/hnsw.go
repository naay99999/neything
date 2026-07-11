package vectorstore

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"sort"
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
	// dirty means the graph no longer matches items and must be rebuilt
	// before searching. Adds are applied incrementally while the graph is
	// clean; deletes always mark it dirty because hnsw.Graph.Delete leaves
	// the graph (and any later export of it) in a state that can panic
	// during search.
	dirty        bool
	itemsUnsaved bool // items differ from the flat vector file on disk
	graphUnsaved bool // graph differs from the graph cache file on disk
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
	if err := s.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load hnsw vectors: %w", err)
		}
		// Brand-new store: start with an empty clean graph so indexing
		// builds it incrementally instead of forcing a rebuild later.
		s.graph = s.newGraph()
		s.dirty = false
	}
	return s, nil
}

func (s *HNSWStore) graphPath() string { return s.path + ".graph" }

func (s *HNSWStore) load() error {
	items, err := loadFlatVectors(s.path)
	if err != nil {
		return err
	}
	s.items = itemsToMap(items)
	s.dirty = true
	if g := s.importGraph(); g != nil {
		s.graph = g
		s.dirty = false
	}
	return nil
}

// importGraph loads the persisted graph cache and validates it against
// items. The graph file is only a cache of the flat vector file; on any
// mismatch it is discarded and the graph is rebuilt lazily.
func (s *HNSWStore) importGraph() *hnsw.Graph[string] {
	f, err := os.Open(s.graphPath())
	if err != nil {
		return nil
	}
	defer f.Close()

	g := s.newGraph()
	if err := g.Import(bufio.NewReaderSize(f, 1<<20)); err != nil {
		return nil
	}
	// Import restores the exported parameters; keep the configured ones.
	g.M = s.m
	g.EfSearch = s.efSearch
	if g.Len() != len(s.items) {
		return nil
	}
	for id := range s.items {
		if _, ok := g.Lookup(id); !ok {
			return nil
		}
	}
	if !probeGraph(g, s.items) {
		return nil
	}
	return g
}

// probeGraph runs one throwaway search to reject cache files whose node
// links are broken (searching such a graph panics deep in the library).
func probeGraph(g *hnsw.Graph[string], items map[string]VectorItem) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	for _, item := range items {
		g.Search(item.Vector, 1)
		break
	}
	return true
}

func (s *HNSWStore) newGraph() *hnsw.Graph[string] {
	g := hnsw.NewGraph[string]()
	g.M = s.m
	g.EfSearch = s.efSearch
	g.Distance = hnsw.CosineDistance
	return g
}

func (s *HNSWStore) rebuildGraphLocked() {
	g := s.newGraph()
	if len(s.items) > 0 {
		nodes := make([]hnsw.Node[string], 0, len(s.items))
		for _, item := range s.items {
			nodes = append(nodes, hnsw.MakeNode(item.ID, item.Vector))
		}
		g.Add(nodes...)
	}
	s.graph = g
	s.dirty = false
	s.graphUnsaved = true
}

func (s *HNSWStore) graphReadyLocked() bool { return !s.dirty && s.graph != nil }

func (s *HNSWStore) Add(_ context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		if s.graphReadyLocked() {
			if _, exists := s.items[item.ID]; exists {
				// Replacing a node needs a graph delete, which is unsafe
				// (see dirty); rebuild lazily instead.
				s.dirty = true
			} else {
				s.graph.Add(hnsw.MakeNode(item.ID, item.Vector))
				s.graphUnsaved = true
			}
		}
		s.items[item.ID] = item
	}
	s.itemsUnsaved = true
	return nil
}

func (s *HNSWStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	s.mu.RLock()
	if s.graphReadyLocked() {
		defer s.mu.RUnlock()
		return s.searchLocked(query, k), nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.graphReadyLocked() {
		s.rebuildGraphLocked()
	}
	return s.searchLocked(query, k), nil
}

func (s *HNSWStore) searchLocked(query []float32, k int) []SearchResult {
	if k <= 0 || len(s.items) == 0 {
		return nil
	}
	if k > len(s.items) {
		k = len(s.items)
	}

	nodes := s.graph.Search(query, k)
	results := make([]SearchResult, 0, len(nodes))
	for _, node := range nodes {
		score := cosineSimilarity(query, node.Value)
		results = append(results, SearchResult{ID: node.Key, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results
}

func (s *HNSWStore) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := false
	for _, id := range ids {
		if _, exists := s.items[id]; !exists {
			continue
		}
		delete(s.items, id)
		removed = true
	}
	if removed {
		s.dirty = true
		s.itemsUnsaved = true
	}
	return nil
}

func (s *HNSWStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.itemsUnsaved {
		if err := saveFlatVectors(s.path, mapToItems(s.items)); err != nil {
			return err
		}
		s.itemsUnsaved = false
	}
	if s.graphReadyLocked() {
		if s.graphUnsaved {
			if err := s.exportGraphLocked(); err != nil {
				// The graph file is only a cache; drop it rather than fail.
				os.Remove(s.graphPath())
			}
			s.graphUnsaved = false
		}
	} else {
		os.Remove(s.graphPath())
		s.graphUnsaved = false
	}
	return nil
}

func (s *HNSWStore) exportGraphLocked() error {
	tmp := s.graphPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	if err := s.graph.Export(w); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.graphPath())
}

func (s *HNSWStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

func (s *HNSWStore) Close() error { return s.Flush() }

func (s *HNSWStore) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	return ids
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
	if err := s.Add(context.Background(), items); err != nil {
		return err
	}
	return s.Close()
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
	return dot / (float32(math.Sqrt(float64(na))) * float32(math.Sqrt(float64(nb))))
}
