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
	mu    sync.RWMutex
	items map[string]VectorItem
	graph *hnsw.Graph[string]
	flat  *flatFile
	m     int

	efSearch int
	// dirty means the graph may not exactly match items: either an
	// existing node was updated in place (hnsw.Graph can't safely update a
	// node's vector) or items were removed (hnsw.Graph.Delete leaves the
	// graph, and any later export of it, in a state that can panic during
	// search, so deletes never touch the graph directly).
	//
	// A dirty graph is still searched directly (never rebuilt on the query
	// path): Search filters out any result ID no longer in items (handles
	// stale deletes) and over-fetches to compensate. Brand-new IDs are
	// still added to the graph incrementally even while dirty (safe, see
	// Add), so they remain searchable without waiting on a rebuild.
	// Rebuilding (clearing dirty) only happens in Flush, off the query
	// path.
	dirty        bool
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
		flat:     newFlatFile(path),
		m:        opts.M,
		efSearch: opts.EfSearch,
		dirty:    true,
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("load hnsw vectors: %w", err)
	}
	if len(s.items) == 0 && s.graph == nil {
		// Brand-new (or empty) store: start with an empty clean graph so
		// indexing builds it incrementally instead of forcing a rebuild
		// later.
		s.graph = s.newGraph()
		s.dirty = false
	}
	return s, nil
}

func (s *HNSWStore) graphPath() string { return s.flat.path + ".graph" }

func (s *HNSWStore) load() error {
	items, err := s.flat.load()
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

// rebuildGraphLocked rebuilds the graph from scratch against the current
// items. It's O(N log N) and must never run on the Search path (see the
// dirty comment); callers are Flush (synchronous, off the query path) and
// ImportBruteForceToHNSW (bulk load, no query path yet).
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

func (s *HNSWStore) Add(_ context.Context, items []VectorItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		if _, exists := s.items[item.ID]; exists {
			// Updating an existing node's vector in place isn't safe;
			// defer to the next rebuild (see dirty).
			s.dirty = true
		} else if s.graph != nil {
			// Adding a brand-new node is safe even on a dirty graph: it
			// doesn't touch any existing node's links.
			s.graph.Add(hnsw.MakeNode(item.ID, item.Vector))
			s.graphUnsaved = true
		}
		s.items[item.ID] = item
		s.flat.stageAdd(item)
	}
	return nil
}

func (s *HNSWStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.searchLocked(query, k), nil
}

func (s *HNSWStore) searchLocked(query []float32, k int) []SearchResult {
	if k <= 0 || len(s.items) == 0 || s.graph == nil || s.graph.Len() == 0 {
		return nil
	}

	// The graph may contain stale nodes: IDs deleted from items but not
	// yet purged from the graph (deletes never touch the graph directly,
	// only rebuild does). Over-fetch by exactly that count so that,
	// after filtering, we still have k live results whenever k live
	// results exist.
	fetchK := k
	if stale := s.graph.Len() - len(s.items); stale > 0 {
		fetchK += stale
	}
	if fetchK > s.graph.Len() {
		fetchK = s.graph.Len()
	}

	nodes := s.graph.Search(query, fetchK)
	results := make([]SearchResult, 0, k)
	for _, node := range nodes {
		if _, live := s.items[node.Key]; !live {
			continue
		}
		results = append(results, SearchResult{ID: node.Key, Score: cosineSimilarity(query, node.Value)})
		if len(results) == k {
			break
		}
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
		s.flat.stageDelete(id)
		removed = true
	}
	if removed {
		s.dirty = true
	}
	return nil
}

// Flush persists pending item changes and, if the graph is dirty (from
// deletes or in-place updates), rebuilds it here -- off the Search path,
// so a query never pays a synchronous O(N log N) rebuild under an
// exclusive lock.
func (s *HNSWStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.flat.flush(func() []VectorItem { return mapToItems(s.items) }); err != nil {
		return err
	}
	if s.dirty {
		s.rebuildGraphLocked()
	}
	if s.graphUnsaved {
		if err := s.exportGraphLocked(); err != nil {
			// The graph file is only a cache; drop it rather than fail.
			os.Remove(s.graphPath())
		}
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

// ImportBruteForceToHNSW migrates a brute-force flat vector file to a new
// HNSW store. It streams the loaded items directly into the target's item
// map (merging with anything already there) and builds the graph with a
// single batched rebuild, rather than one Add-with-lock per item.
func ImportBruteForceToHNSW(brutePath, hnswPath string, opts HNSWOptions) error {
	items, err := newFlatFile(brutePath).load()
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

	s.mu.Lock()
	for _, item := range items {
		s.items[item.ID] = item
		s.flat.stageAdd(item)
	}
	items = nil // release the loaded slice; s.items now owns the only references
	s.dirty = true
	s.rebuildGraphLocked()
	s.mu.Unlock()

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
