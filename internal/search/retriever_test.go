package search

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

// stubEmbedder returns a fixed query vector, or an error when errOnEmbed is
// set — used to simulate an embedder endpoint being down.
type stubEmbedder struct {
	vec        []float32
	errOnEmbed error
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if s.errOnEmbed != nil {
		return nil, s.errOnEmbed
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = s.vec
	}
	return out, nil
}

func (s *stubEmbedder) Dimensions() int { return len(s.vec) }
func (s *stubEmbedder) ModelID() string { return "stub" }

// seedRetrieverDB builds a small two-chunk corpus in a fresh SQLite DB:
// chunk 1's content overlaps the query tokens strongly (FTS + exact-vector
// match), chunk 2 is unrelated filler. Returns the DB and the two chunk IDs
// in insertion order.
func seedRetrieverDB(t *testing.T) (*store.DB, []int64) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	wsID, err := db.UpsertWorkspace("test", dir)
	if err != nil {
		t.Fatal(err)
	}
	docID, err := db.UpsertDocument(&store.Document{
		WorkspaceID: wsID,
		Path:        filepath.Join(dir, "a.md"),
		Type:        "md",
		Hash:        "h1",
	})
	if err != nil {
		t.Fatal(err)
	}

	chunks := []*store.Chunk{
		{DocumentID: docID, ChunkIndex: 0, Content: "billing uses stripe subscriptions for recurring revenue", StartPos: 0, EndPos: 10},
		{DocumentID: docID, ChunkIndex: 1, Content: "unrelated gardening tips about tomatoes", StartPos: 10, EndPos: 20},
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertChunks(tx, chunks); err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if err := db.UpsertChunkFTS(tx, c.ID, c.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	return db, []int64{chunks[0].ID, chunks[1].ID}
}

// TestRetriever_AutoModeMatrix covers {embedder nil, vectors empty, embed
// error, healthy} x {auto, semantic, keyword, hybrid}.
func TestRetriever_AutoModeMatrix(t *testing.T) {
	const query = "billing uses stripe subscriptions for recurring revenue"
	queryVec := []float32{1, 0, 0, 0}
	matchVec := []float32{1, 0, 0, 0} // parallel to queryVec -> cosine 1
	otherVec := []float32{0, 1, 0, 0} // orthogonal -> cosine 0

	type setup struct {
		name         string
		embedderNil  bool
		vectorsEmpty bool
		embedErr     bool
	}
	setups := []setup{
		{name: "embedder nil", embedderNil: true},
		{name: "vectors empty", vectorsEmpty: true},
		{name: "embed error", embedErr: true},
		{name: "healthy"},
	}

	modes := []string{ModeAuto, ModeSemantic, ModeKeyword, ModeHybrid}

	for _, s := range setups {
		for _, mode := range modes {
			t.Run(s.name+"/"+mode, func(t *testing.T) {
				db, chunkIDs := seedRetrieverDB(t)

				dir := t.TempDir()
				vs, err := vectorstore.NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { vs.Close() })

				if !s.vectorsEmpty {
					items := []vectorstore.VectorItem{
						{ID: strconv.FormatInt(chunkIDs[0], 10), Vector: matchVec},
						{ID: strconv.FormatInt(chunkIDs[1], 10), Vector: otherVec},
					}
					if err := vs.Add(context.Background(), items); err != nil {
						t.Fatal(err)
					}
				}

				r := &Retriever{DB: db, Vectors: vs}
				if !s.embedderNil {
					emb := &stubEmbedder{vec: queryVec}
					if s.embedErr {
						emb.errOnEmbed = errors.New("embedder endpoint down")
					}
					r.Embedder = emb
				}

				results, meta, err := r.Search(context.Background(), query, RetrieveOptions{
					TopK: 5, FetchK: 10, Mode: mode,
				})

				semanticAvailable := !s.embedderNil && !s.vectorsEmpty
				wantHardError := (mode == ModeSemantic || mode == ModeHybrid) && (!semanticAvailable || s.embedErr)

				if wantHardError {
					if err == nil {
						t.Fatalf("expected error for mode=%s setup=%s, got results=%v meta=%+v", mode, s.name, results, meta)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error for mode=%s setup=%s: %v", mode, s.name, err)
				}

				wantKeywordUsed := mode == ModeAuto || mode == ModeKeyword || mode == ModeHybrid
				wantSemanticUsed := (mode == ModeAuto || mode == ModeSemantic || mode == ModeHybrid) && semanticAvailable && !s.embedErr

				if meta.KeywordUsed != wantKeywordUsed {
					t.Errorf("mode=%s setup=%s: KeywordUsed = %v, want %v", mode, s.name, meta.KeywordUsed, wantKeywordUsed)
				}
				if meta.SemanticUsed != wantSemanticUsed {
					t.Errorf("mode=%s setup=%s: SemanticUsed = %v, want %v", mode, s.name, meta.SemanticUsed, wantSemanticUsed)
				}

				wantDegraded := mode == ModeAuto && (!semanticAvailable || s.embedErr)
				if wantDegraded && meta.Degraded == "" {
					t.Errorf("mode=%s setup=%s: expected meta.Degraded to be set, got empty", mode, s.name)
				}
				if !wantDegraded && meta.Degraded != "" {
					t.Errorf("mode=%s setup=%s: expected no degradation, got %q", mode, s.name, meta.Degraded)
				}

				if len(results) == 0 {
					t.Fatalf("mode=%s setup=%s: expected results, got none", mode, s.name)
				}
				if results[0].ChunkID != chunkIDs[0] {
					t.Errorf("mode=%s setup=%s: expected top result chunk %d, got %d", mode, s.name, chunkIDs[0], results[0].ChunkID)
				}

				wantCoverage := 0.0
				if !s.vectorsEmpty {
					wantCoverage = 1.0
				}
				if meta.EmbedCoverage != wantCoverage {
					t.Errorf("mode=%s setup=%s: EmbedCoverage = %v, want %v", mode, s.name, meta.EmbedCoverage, wantCoverage)
				}
			})
		}
	}
}

// TestRetriever_ZeroSignalsReturnsEmpty covers the "nothing available at
// all" case explicitly: no embedder, empty FTS index (no chunks).
func TestRetriever_ZeroSignalsReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	vs, err := vectorstore.NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer vs.Close()

	r := &Retriever{DB: db, Vectors: vs}
	results, meta, err := r.Search(context.Background(), "anything", RetrieveOptions{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
	if meta.SemanticUsed || meta.KeywordUsed {
		t.Fatalf("expected no signals used, got meta=%+v", meta)
	}
}

// countingChunkStore is a chunkStore stub that counts calls to
// GetChunksByIDs/GetDocumentsByChunkIDs, used to pin down the fix-1
// restructuring: hydration must happen exactly once per Search call, even
// when the keyword and semantic legs return overlapping chunk IDs.
type countingChunkStore struct {
	fts    []store.FTSResult
	chunks []*store.Chunk
	docs   map[int64]*store.DocWithWorkspace

	getChunksCalls int
	getDocsCalls   int
}

func (c *countingChunkStore) SearchFTS(_ string, _ int) ([]store.FTSResult, error) {
	return c.fts, nil
}

func (c *countingChunkStore) GetChunksByIDs(_ []int64) ([]*store.Chunk, error) {
	c.getChunksCalls++
	return c.chunks, nil
}

func (c *countingChunkStore) GetDocumentsByChunkIDs(_ []int64) (map[int64]*store.DocWithWorkspace, error) {
	c.getDocsCalls++
	return c.docs, nil
}

func (c *countingChunkStore) CountChunks() (int, error) { return len(c.chunks), nil }

func TestRetriever_HydratesOnceForOverlappingIDs(t *testing.T) {
	docs := map[int64]*store.DocWithWorkspace{
		1: {Document: store.Document{ID: 10, Path: "/a.md", Type: "md"}, WorkspaceName: "ws"},
		2: {Document: store.Document{ID: 10, Path: "/a.md", Type: "md"}, WorkspaceName: "ws"},
	}
	chunks := []*store.Chunk{
		{ID: 1, Content: "billing stripe subscription"},
		{ID: 2, Content: "gardening tomatoes"},
	}
	cs := &countingChunkStore{
		fts:    []store.FTSResult{{ChunkID: 1, Score: 2.0}, {ChunkID: 2, Score: 1.0}},
		chunks: chunks,
		docs:   docs,
	}

	dir := t.TempDir()
	vs, err := vectorstore.NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer vs.Close()

	// Same two chunk IDs as the FTS leg above — the overlap case fix 1
	// targets — just in a different score order.
	items := []vectorstore.VectorItem{
		{ID: "1", Vector: []float32{1, 0}},
		{ID: "2", Vector: []float32{0.9, 0.1}},
	}
	if err := vs.Add(context.Background(), items); err != nil {
		t.Fatal(err)
	}

	r := &Retriever{
		DB:       cs,
		Vectors:  vs,
		Embedder: &stubEmbedder{vec: []float32{1, 0}},
	}

	results, meta, err := r.Search(context.Background(), "billing stripe", RetrieveOptions{
		TopK: 5, FetchK: 10, Mode: ModeHybrid,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.SemanticUsed || !meta.KeywordUsed {
		t.Fatalf("expected both signals used, got meta=%+v", meta)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 hydrated results, got %d: %+v", len(results), results)
	}
	if cs.getChunksCalls != 1 {
		t.Errorf("expected GetChunksByIDs called exactly once for the fused set, got %d calls", cs.getChunksCalls)
	}
	if cs.getDocsCalls != 1 {
		t.Errorf("expected GetDocumentsByChunkIDs called exactly once for the fused set, got %d calls", cs.getDocsCalls)
	}
}

// TestRetriever_EmptyModeBehavesLikeAuto ensures a zero-value Mode (as
// produced by structs built without setting it) doesn't hard-fail like
// semantic/hybrid would when the embedder is unavailable.
func TestRetriever_EmptyModeBehavesLikeAuto(t *testing.T) {
	db, chunkIDs := seedRetrieverDB(t)
	dir := t.TempDir()
	vs, err := vectorstore.NewBruteForceStore(filepath.Join(dir, "vectors.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer vs.Close()

	r := &Retriever{DB: db, Vectors: vs} // no embedder, empty Mode
	results, meta, err := r.Search(context.Background(), "billing stripe", RetrieveOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.KeywordUsed || meta.SemanticUsed {
		t.Fatalf("expected keyword-only degrade, got meta=%+v", meta)
	}
	if meta.Degraded == "" {
		t.Fatal("expected degraded note for missing embedder")
	}
	if len(results) == 0 || results[0].ChunkID != chunkIDs[0] {
		t.Fatalf("expected top result chunk %d, got %v", chunkIDs[0], results)
	}
}
