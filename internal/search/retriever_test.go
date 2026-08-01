package search

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/store"
)

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

// TestRetriever_KeywordSearch covers the one path that exists now: FTS
// matches rank above unrelated filler, and meta reports keyword_used.
func TestRetriever_KeywordSearch(t *testing.T) {
	db, ids := seedRetrieverDB(t)

	r := &Retriever{DB: db}
	results, meta, err := r.Search(context.Background(), "billing stripe subscriptions",
		RetrieveOptions{TopK: 5, FetchK: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.KeywordUsed {
		t.Error("expected meta.KeywordUsed")
	}
	if meta.Degraded != "" {
		t.Errorf("expected no degradation on a healthy FTS index, got %q", meta.Degraded)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].ChunkID != ids[0] {
		t.Errorf("expected the billing chunk (id=%d) first, got id=%d", ids[0], results[0].ChunkID)
	}
}

// TestRetriever_NoMatchReturnsEmpty: a query matching nothing is not an
// error — MCP callers supplement empty results with a live scan.
func TestRetriever_NoMatchReturnsEmpty(t *testing.T) {
	db, _ := seedRetrieverDB(t)

	r := &Retriever{DB: db}
	results, meta, err := r.Search(context.Background(), "zzzznotawordanywhere", RetrieveOptions{TopK: 5})
	if err != nil {
		t.Fatalf("expected no error for an empty result set, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
	if meta.KeywordUsed {
		t.Error("expected KeywordUsed=false when FTS returned nothing")
	}
}

// countingChunkStore is a chunkStore stub that counts calls to
// GetChunksByIDs/GetDocumentsByChunkIDs, pinning down the hydration
// restructuring: content and document metadata are fetched exactly once for
// the whole ranked ID set, not once per result.
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

func TestRetriever_HydratesOncePerSearch(t *testing.T) {
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

	r := &Retriever{DB: cs}

	results, meta, err := r.Search(context.Background(), "billing stripe", RetrieveOptions{
		TopK: 5, FetchK: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.KeywordUsed {
		t.Fatalf("expected keyword signal used, got meta=%+v", meta)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 hydrated results, got %d: %+v", len(results), results)
	}
	if cs.getChunksCalls != 1 {
		t.Errorf("expected GetChunksByIDs called exactly once for the ranked set, got %d calls", cs.getChunksCalls)
	}
	if cs.getDocsCalls != 1 {
		t.Errorf("expected GetDocumentsByChunkIDs called exactly once for the ranked set, got %d calls", cs.getDocsCalls)
	}
}

// TestHasPathPrefixRespectsComponentBoundaries: path_prefix is a scoping
// filter, and a plain string prefix silently widened it — /a/proj also
// matched the unrelated /a/project-private.
func TestHasPathPrefixRespectsComponentBoundaries(t *testing.T) {
	sep := string(filepath.Separator)
	join := func(parts ...string) string { return sep + strings.Join(parts, sep) }

	cases := []struct {
		path, prefix string
		want         bool
	}{
		{join("a", "proj", "notes.md"), join("a", "proj"), true},
		{join("a", "proj"), join("a", "proj"), true},
		{join("a", "proj", "deep", "x.md"), join("a", "proj"), true},
		{join("a", "proj", "notes.md"), join("a", "proj") + sep, true},
		{join("a", "project-private", "secrets.md"), join("a", "proj"), false},
		{join("a", "projector.md"), join("a", "proj"), false},
		{join("b", "proj", "notes.md"), join("a", "proj"), false},
		{join("a", "proj", "notes.md"), "", true},
	}
	for _, tc := range cases {
		if got := hasPathPrefix(tc.path, tc.prefix); got != tc.want {
			t.Errorf("hasPathPrefix(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
		}
	}
}
