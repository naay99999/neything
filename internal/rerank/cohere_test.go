package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCohereReranker_ReordersByScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.95},
				{"index": 0, "relevance_score": 0.40},
			},
		})
	}))
	defer server.Close()

	r := NewCohereReranker("test-key", "rerank-v3.5")
	r.BaseURL = server.URL
	r.client = server.Client()

	out, err := r.Rerank(context.Background(), "query", []Candidate{
		{ChunkID: 1, Content: "first"},
		{ChunkID: 2, Content: "second"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
	if out[0].ChunkID != 2 {
		t.Fatalf("expected chunk 2 first, got %d", out[0].ChunkID)
	}
}

func TestJinaReranker_ReordersByScore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.88},
			},
		})
	}))
	defer server.Close()

	r := NewJinaReranker("test-key", "jina-reranker-v2-base-multilingual")
	r.BaseURL = server.URL
	r.client = server.Client()

	out, err := r.Rerank(context.Background(), "query", []Candidate{
		{ChunkID: 5, Content: "only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ChunkID != 5 {
		t.Fatalf("unexpected result: %+v", out)
	}
}
