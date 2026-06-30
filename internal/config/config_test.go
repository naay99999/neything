package config

import "testing"

func TestValidateRejectsClaudeEmbedder(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chat:     ChatConfig{Provider: "openai", Model: "gpt-4o"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for claude embedder")
	}
}

func TestValidateAcceptsValidProviders(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidVectorStore(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "markdown"},
		VectorStore: VectorStoreConfig{Backend: "faiss"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid vector store backend")
	}
}

func TestValidateRequiresModels(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "ollama", Model: ""},
		Chat:     ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty embedder model")
	}
}

func TestValidateRerankRequiresProvider(t *testing.T) {
	cfg := &Config{
		Embedder:  EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:      ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Retrieval: RetrievalConfig{Rerank: true},
		Reranker:  RerankerConfig{Provider: "invalid", Model: "x"},
		Chunking:  ChunkingConfig{Strategy: "markdown"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid reranker provider")
	}
}

func TestValidateAutoChunkingByFormat(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"pdf": "page"}},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAutoRejectsInvalidByFormat(t *testing.T) {
	cfg := &Config{
		Embedder:    EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:        ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chunking:    ChunkingConfig{Strategy: "auto", ByFormat: map[string]string{"pdf": "invalid"}},
		VectorStore: VectorStoreConfig{Backend: "brute"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid by_format strategy")
	}
}

func TestFetchKUsesRerankTopK(t *testing.T) {
	cfg := &Config{
		Retrieval: RetrievalConfig{TopK: 8, RerankTopK: 30},
	}
	if got := FetchK(cfg, 8); got != 30 {
		t.Fatalf("expected fetchK 30, got %d", got)
	}
}
