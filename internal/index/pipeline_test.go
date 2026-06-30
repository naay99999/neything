package index

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/search"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

const testDim = 16

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = hashToVector(text, testDim)
	}
	return out, nil
}

func (m *mockEmbedder) Dimensions() int { return testDim }
func (m *mockEmbedder) ModelID() string { return "mock-embedder" }

func hashToVector(text string, dim int) []float32 {
	h := fnv.New64a()
	h.Write([]byte(text))
	seed := h.Sum64()
	vec := make([]float32, dim)
	for i := range vec {
		seed = seed*6364136223846793005 + 1
		vec[i] = float32(int(seed>>33)%1000) / 1000
	}
	// normalize
	var sum float32
	for _, v := range vec {
		sum += v * v
	}
	if sum > 0 {
		n := float32(1.0 / float32(sum))
		for i := range vec {
			vec[i] *= n
		}
	}
	return vec
}

func setupIndexer(t *testing.T) (*Indexer, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	vecPath := filepath.Join(dir, "vectors.bin")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := vectorstore.NewBruteForceStore(vecPath)
	if err != nil {
		t.Fatal(err)
	}
	chunker, err := chunk.NewChunker("character", 200, 20)
	if err != nil {
		t.Fatal(err)
	}
	ix := &Indexer{
		DB:        db,
		Vectors:   vs,
		Embedder:  &mockEmbedder{},
		Loaders:   loader.NewRegistry(&loader.MarkdownLoader{}),
		Chunker:   chunker,
		BatchSize: 8,
	}
	return ix, db, dir
}

func TestIndexerIndexAndSearch(t *testing.T) {
	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	unique := "billing uses stripe subscriptions for recurring revenue"
	if err := os.WriteFile(filepath.Join(root, "billing.md"), []byte(unique+"\nextra notes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.md"), []byte("unrelated gardening tips"), 0644); err != nil {
		t.Fatal(err)
	}

	stats, err := ix.Index(context.Background(), root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 2 {
		t.Fatalf("expected 2 files scanned, got %d", stats.FilesScanned)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}

	retriever := &search.Retriever{
		DB:       db,
		Vectors:  ix.Vectors,
		Embedder: &mockEmbedder{},
	}
	results, err := retriever.Search(context.Background(), unique, 3, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].DocPath != filepath.Join(root, "billing.md") {
		t.Fatalf("expected billing.md top result, got %s", results[0].DocPath)
	}
}
